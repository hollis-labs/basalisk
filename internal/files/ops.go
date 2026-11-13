package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// File operations: create, rename and move, copy, delete, search. Every path is checked
// against its root as reads are, and nothing is ever overwritten: a name that is taken is
// ErrExists, for the caller to ask about.

// ErrExists means the target name is taken.
var ErrExists = errors.New("an item with that name already exists")

// ErrTooMany means a copy or search went past its bound.
var ErrTooMany = errors.New("too many items")

const (
	maxCopyItems   = 20000
	maxSearchSeen  = 200000
	maxSearchFound = 200
	searchTime     = 5 * time.Second
)

// skipDirs are not searched unless the search starts inside one.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

// opsBackend is what a backend adds for operations.
type opsBackend interface {
	Mkdir(ctx context.Context, rel string) (Entry, error)
	// CreateFrom makes a new file from r; it refuses an existing name.
	CreateFrom(ctx context.Context, rel string, r io.Reader) (Entry, error)
	// Rename moves within the root; it refuses an existing target.
	Rename(ctx context.Context, from, to string) (Entry, error)
	// Delete removes rel: to the Trash where there is one (trashed is true), else for good.
	Delete(ctx context.Context, rel string) (trashed bool, err error)
	// Walk calls fn for everything under rel (not rel itself), depth first; fn returns false
	// to skip a folder's contents. It stops when ctx ends.
	Walk(ctx context.Context, rel string, fn func(Entry) bool) error
}

func (m *Manager) ops(root string) (backend, opsBackend, error) {
	b, err := m.backend(root)
	if err != nil {
		return nil, nil, err
	}
	o, ok := b.(opsBackend)
	if !ok {
		return nil, nil, errors.New("this place does not support changes")
	}
	return b, o, nil
}

// validName checks a single path element the person chose.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00") && len(name) <= 255
}

func (m *Manager) target(root, p string) (backend, opsBackend, string, error) {
	b, o, err := m.ops(root)
	if err != nil {
		return nil, nil, "", err
	}
	rel, err := Clean(p)
	if err != nil {
		return nil, nil, "", err
	}
	if rel == "" || !validName(path.Base(rel)) {
		return nil, nil, "", ErrBadPath
	}
	return b, o, rel, nil
}

// Mkdir makes a new folder.
func (m *Manager) Mkdir(ctx context.Context, root, p string) (Entry, error) {
	_, o, rel, err := m.target(root, p)
	if err != nil {
		return Entry{}, err
	}
	return o.Mkdir(ctx, rel)
}

// Create makes a new empty file.
func (m *Manager) Create(ctx context.Context, root, p string) (Entry, error) {
	_, o, rel, err := m.target(root, p)
	if err != nil {
		return Entry{}, err
	}
	return o.CreateFrom(ctx, rel, strings.NewReader(""))
}

// Ref names a file or folder in a root.
type Ref struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// Move renames or moves from to to (another folder, or another place).
func (m *Manager) Move(ctx context.Context, from, to Ref) (Entry, error) {
	sb, so, src, err := m.target(from.Root, from.Path)
	if err != nil {
		return Entry{}, err
	}
	_, do, dst, err := m.target(to.Root, to.Path)
	if err != nil {
		return Entry{}, err
	}
	if from.Root == to.Root {
		if dst == src || strings.HasPrefix(dst, src+"/") {
			return Entry{}, fmt.Errorf("%w: a folder cannot move into itself", ErrBadPath)
		}
		return so.Rename(ctx, src, dst)
	}
	// Across places: copy, then remove the original (to the Trash where there is one).
	e, err := m.copyAcross(ctx, sb, src, do, dst)
	if err != nil {
		return Entry{}, err
	}
	if _, err = so.Delete(ctx, src); err != nil {
		return e, err
	}
	return e, nil
}

// Copy copies from to to, folders with their contents.
func (m *Manager) Copy(ctx context.Context, from, to Ref) (Entry, error) {
	sb, _, src, err := m.target(from.Root, from.Path)
	if err != nil {
		return Entry{}, err
	}
	_, do, dst, err := m.target(to.Root, to.Path)
	if err != nil {
		return Entry{}, err
	}
	if from.Root == to.Root && (dst == src || strings.HasPrefix(dst, src+"/")) {
		return Entry{}, fmt.Errorf("%w: a folder cannot be copied into itself", ErrBadPath)
	}
	return m.copyAcross(ctx, sb, src, do, dst)
}

// copyAcross copies through the backends' own reads and writes, so it works within a place
// and between places alike. Links are followed; a broken one is skipped.
func (m *Manager) copyAcross(ctx context.Context, sb backend, src string, do opsBackend, dst string) (Entry, error) {
	count := 0
	var walk func(src, dst string) (Entry, error)
	walk = func(src, dst string) (Entry, error) {
		if count++; count > maxCopyItems {
			return Entry{}, ErrTooMany
		}
		if err := ctx.Err(); err != nil {
			return Entry{}, err
		}
		e, err := sb.Stat(ctx, src)
		if err != nil {
			return Entry{}, err
		}
		if e.Broken {
			return Entry{}, nil
		}
		if !e.Dir {
			f, _, oerr := sb.Open(ctx, src)
			if oerr != nil {
				return Entry{}, oerr
			}
			defer func() { _ = f.Close() }()
			return do.CreateFrom(ctx, dst, f)
		}
		made, err := do.Mkdir(ctx, dst)
		if err != nil {
			return Entry{}, err
		}
		children, err := sb.List(ctx, src)
		if err != nil {
			return Entry{}, err
		}
		for _, c := range children {
			if c.Broken {
				continue
			}
			if _, err = walk(c.Path, join(dst, c.Name)); err != nil {
				return Entry{}, err
			}
		}
		return made, nil
	}
	return walk(src, dst)
}

// Delete removes an item: to the Trash for a place on this machine, for good on a remote one.
func (m *Manager) Delete(ctx context.Context, root, p string) (bool, error) {
	_, o, rel, err := m.target(root, p)
	if err != nil {
		return false, err
	}
	return o.Delete(ctx, rel)
}

// SearchResult is a bounded search's answer.
type SearchResult struct {
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// Search finds files and folders under p whose name holds every word of q (case
// insensitive). It skips .git and node_modules unless the search starts inside one, and
// stops at maxSearchFound results, maxSearchSeen items or searchTime.
func (m *Manager) Search(ctx context.Context, root, p, q string) (SearchResult, error) {
	b, err := m.backend(root)
	if err != nil {
		return SearchResult{}, err
	}
	o, ok := b.(opsBackend)
	if !ok {
		return SearchResult{}, errors.New("this place cannot be searched")
	}
	rel, err := Clean(p)
	if err != nil {
		return SearchResult{}, err
	}
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return SearchResult{Entries: []Entry{}}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, searchTime)
	defer cancel()
	res := SearchResult{Entries: []Entry{}}
	seen := 0
	err = o.Walk(ctx, rel, func(e Entry) bool {
		if seen++; seen > maxSearchSeen || len(res.Entries) >= maxSearchFound {
			res.Truncated = true
			cancel()
			return false
		}
		name := strings.ToLower(e.Name)
		hit := true
		for _, w := range words {
			if !strings.Contains(name, w) {
				hit = false
				break
			}
		}
		if hit {
			res.Entries = append(res.Entries, e)
		}
		return !e.Dir || !skipDirs[e.Name]
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		res.Truncated = true
		err = nil
	}
	return res, err
}
