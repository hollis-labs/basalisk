package files

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"
)

// The notes index: every markdown or text file under a folder, so Notes can resolve
// [[wiki links]] by name. Bounded: maxNoteFiles files, searchTime.

const maxNoteFiles = 5000

var noteExts = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".txt": true}

// NoteFile is one note in the index.
type NoteFile struct {
	Path  string    `json:"path"`
	MTime time.Time `json:"mtime"`
}

// NoteIndex is the notes under a folder; Truncated says it stopped early.
type NoteIndex struct {
	Notes     []NoteFile `json:"notes"`
	Truncated bool       `json:"truncated"`
}

// Notes lists the markdown and text files under p (not in .git, node_modules or dot folders).
func (m *Manager) Notes(ctx context.Context, root, p string) (NoteIndex, error) {
	b, err := m.backend(root)
	if err != nil {
		return NoteIndex{}, err
	}
	o, ok := b.(opsBackend)
	if !ok {
		return NoteIndex{}, errors.New("this place cannot be indexed")
	}
	rel, err := Clean(p)
	if err != nil {
		return NoteIndex{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchTime)
	defer cancel()
	res := NoteIndex{Notes: []NoteFile{}}
	err = o.Walk(ctx, rel, func(e Entry) bool {
		if e.Dir {
			return !skipDirs[e.Name] && !strings.HasPrefix(e.Name, ".")
		}
		if len(res.Notes) >= maxNoteFiles {
			res.Truncated = true
			cancel()
			return false
		}
		if noteExts[strings.ToLower(path.Ext(e.Name))] {
			res.Notes = append(res.Notes, NoteFile{Path: e.Path, MTime: e.Mtime})
		}
		return true
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		res.Truncated, err = true, nil
	}
	return res, err
}
