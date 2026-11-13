// Package files serves folders on this machine and on remote hosts as a small set of roots.
//
// A root is an allowlisted folder: a local one (the home directory, ~/dev, ~/Downloads...)
// or a remote one reached over SFTP through the user's own ssh setup. Every path the API
// takes is relative to a root, and nothing that resolves outside its root is served,
// symbolic links included.
package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors the API maps to statuses.
var (
	ErrNoRoot   = errors.New("no such root")
	ErrOutside  = errors.New("path is outside its root")
	ErrNotFound = errors.New("no such file or folder")
	ErrNotDir   = errors.New("not a folder")
	ErrIsDir    = errors.New("is a folder")
	ErrBadPath  = errors.New("invalid path")
	ErrConflict = errors.New("the file changed since it was read")
	ErrRemote   = errors.New("the remote host could not be reached")
)

// Root describes a served folder.
type Root struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // local | sftp
	Host  string `json:"host,omitempty"`
	Path  string `json:"path"`
}

// Entry describes a file or folder. Path is relative to its root, slash separated, and ""
// for the root itself.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Mtime   time.Time `json:"mtime"`
	Mode    string    `json:"mode"`
	Symlink bool      `json:"symlink"`
	// Broken marks a link whose target is missing or outside the root.
	Broken bool `json:"broken,omitempty"`
}

// ETag is a validator for a file's content: it changes when the file is rewritten.
func (e Entry) ETag() string {
	return `"` + strings.Join([]string{itoa(e.Size), itoa(e.Mtime.UnixNano())}, "-") + `"`
}

// Content is an open file.
type Content interface {
	io.ReadSeeker
	io.Closer
}

// backend is one root's file system. rel is already cleaned and checked: "" or a
// slash-separated path without "." or ".." elements.
type backend interface {
	Stat(ctx context.Context, rel string) (Entry, error)
	List(ctx context.Context, rel string) ([]Entry, error)
	Open(ctx context.Context, rel string) (Content, Entry, error)
	// Write replaces the file's content, or creates it. ifMatch, when set, must be the
	// current ETag.
	Write(ctx context.Context, rel string, body io.Reader, ifMatch string) (Entry, error)
	// LocalPath is the folder on this machine, for "open a terminal here"; "" when remote.
	LocalPath(rel string) string
	Close() error
}

// Manager holds the roots.
type Manager struct {
	mu    sync.RWMutex
	roots []Root
	by    map[string]backend
	// cfg and cfgFile let remote places be added and removed at run time (places.go).
	cfg     Config
	cfgFile string
	home    string
}

// Hosts lists the concrete hosts in the owner's ssh config, for adding a remote place.
func (m *Manager) Hosts() []string { return SSHHosts(m.home) }

// Roots lists the served roots in order.
func (m *Manager) Roots() []Root {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Root(nil), m.roots...)
}

func (m *Manager) backend(id string) (backend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.by[id]
	if !ok {
		return nil, ErrNoRoot
	}
	return b, nil
}

// Clean checks a root-relative path and returns its canonical form.
func Clean(p string) (string, error) {
	if strings.ContainsRune(p, 0) || strings.Contains(p, "\\") {
		return "", ErrBadPath
	}
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return "", ErrOutside
		}
	}
	c := path.Clean(p)
	if c == "." {
		return "", nil
	}
	return c, nil
}

func (m *Manager) Stat(ctx context.Context, root, p string) (Entry, error) {
	b, rel, err := m.resolve(root, p)
	if err != nil {
		return Entry{}, err
	}
	return b.Stat(ctx, rel)
}

func (m *Manager) List(ctx context.Context, root, p string) ([]Entry, error) {
	b, rel, err := m.resolve(root, p)
	if err != nil {
		return nil, err
	}
	list, err := b.List(ctx, rel)
	if err != nil {
		return nil, err
	}
	sortEntries(list)
	return list, nil
}

func (m *Manager) Open(ctx context.Context, root, p string) (Content, Entry, error) {
	b, rel, err := m.resolve(root, p)
	if err != nil {
		return nil, Entry{}, err
	}
	return b.Open(ctx, rel)
}

func (m *Manager) Write(ctx context.Context, root, p string, body io.Reader, ifMatch string) (Entry, error) {
	b, rel, err := m.resolve(root, p)
	if err != nil {
		return Entry{}, err
	}
	if rel == "" {
		return Entry{}, ErrIsDir
	}
	return b.Write(ctx, rel, body, ifMatch)
}

// LocalPath is the folder on this machine a root path names, or "" for a remote root.
func (m *Manager) LocalPath(root, p string) (string, error) {
	b, rel, err := m.resolve(root, p)
	if err != nil {
		return "", err
	}
	return b.LocalPath(rel), nil
}

// Close disconnects the remote roots.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.by {
		_ = b.Close()
	}
}

func (m *Manager) resolve(root, p string) (backend, string, error) {
	b, err := m.backend(root)
	if err != nil {
		return nil, "", err
	}
	rel, err := Clean(p)
	if err != nil {
		return nil, "", err
	}
	return b, rel, nil
}

// Folders first, then by name, case-insensitively.
func sortEntries(list []Entry) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].Name < list[j].Name
	})
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

func entryFrom(rel string, fi fs.FileInfo) Entry {
	name := path.Base(rel)
	if rel == "" {
		name = ""
	}
	return Entry{
		Name:  name,
		Path:  rel,
		Dir:   fi.IsDir(),
		Size:  sizeOf(fi),
		Mtime: fi.ModTime().UTC(),
		Mode:  fi.Mode().String(),
	}
}

func sizeOf(fi fs.FileInfo) int64 {
	if fi.IsDir() {
		return 0
	}
	return fi.Size()
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
