package files

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// local is a folder on this machine. Every access goes through an os.Root, which refuses a
// path or a symbolic link that leads outside the folder.
type local struct {
	dir   string
	root  *os.Root
	trash string // where Delete moves things (the owner's ~/.Trash)
}

func openLocal(dir string) (*local, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &local{dir: dir, root: r}, nil
}

func name(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// stat describes rel, following a link when it stays inside the root.
func (l *local) stat(rel string) (Entry, error) {
	fi, err := l.root.Lstat(name(rel))
	if err != nil {
		return Entry{}, localErr(err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return entryFrom(rel, fi), nil
	}
	target, err := l.root.Stat(name(rel))
	if err != nil {
		e := entryFrom(rel, fi)
		e.Symlink, e.Broken, e.Size = true, true, 0
		return e, nil //nolint:nilerr // a dangling or escaping link is listed, marked broken
	}
	e := entryFrom(rel, target)
	e.Symlink = true
	return e, nil
}

func (l *local) Stat(_ context.Context, rel string) (Entry, error) { return l.stat(rel) }

func (l *local) List(_ context.Context, rel string) ([]Entry, error) {
	e, err := l.stat(rel)
	if err != nil {
		return nil, err
	}
	if !e.Dir || e.Broken {
		return nil, ErrNotDir
	}
	des, err := fs.ReadDir(l.root.FS(), name(rel))
	if err != nil {
		return nil, localErr(err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		c, err := l.stat(join(rel, de.Name()))
		if err != nil {
			continue // removed while listing, or unreadable
		}
		out = append(out, c)
	}
	return out, nil
}

func (l *local) Open(_ context.Context, rel string) (Content, Entry, error) {
	e, err := l.stat(rel)
	if err != nil {
		return nil, Entry{}, err
	}
	if e.Broken {
		return nil, Entry{}, ErrNotFound
	}
	if e.Dir {
		return nil, Entry{}, ErrIsDir
	}
	f, err := l.root.Open(name(rel))
	if err != nil {
		return nil, Entry{}, localErr(err)
	}
	return f, e, nil
}

func (l *local) Write(_ context.Context, rel string, body io.Reader, ifMatch string) (Entry, error) {
	cur, err := l.stat(rel)
	exists := err == nil
	switch {
	case err != nil && !errors.Is(err, ErrNotFound):
		return Entry{}, err
	case exists && cur.Dir:
		return Entry{}, ErrIsDir
	case ifMatch != "" && (!exists || cur.ETag() != ifMatch):
		return Entry{}, ErrConflict
	}
	// A link is written through, in place, so it stays a link (os.Root still refuses one
	// that leads outside). Anything else is replaced atomically: a temporary file beside
	// it, then a rename, so a reader never sees half a file.
	if exists && cur.Symlink {
		lf, lerr := l.root.OpenFile(rel, os.O_WRONLY|os.O_TRUNC, 0)
		if lerr != nil {
			return Entry{}, localErr(lerr)
		}
		if _, lerr = io.Copy(lf, body); lerr != nil {
			_ = lf.Close()
			return Entry{}, lerr
		}
		if lerr = lf.Close(); lerr != nil {
			return Entry{}, lerr
		}
		return l.stat(rel)
	}
	perm := fs.FileMode(0o644)
	if exists {
		if fi, serr := l.root.Lstat(rel); serr == nil {
			perm = fi.Mode().Perm()
		}
	}
	tmp := path.Join(path.Dir(rel), "."+path.Base(rel)+".basalisk-"+rand.Text()[:8])
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return Entry{}, localErr(err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = l.root.Remove(tmp)
		}
	}()
	if _, err = io.Copy(f, body); err != nil {
		_ = f.Close()
		return Entry{}, err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return Entry{}, err
	}
	if err = f.Close(); err != nil {
		return Entry{}, err
	}
	if err = l.root.Chmod(tmp, perm); err != nil {
		return Entry{}, err
	}
	if err = l.root.Rename(tmp, rel); err != nil {
		return Entry{}, localErr(err)
	}
	ok = true
	return l.stat(rel)
}

func (l *local) LocalPath(rel string) string { return filepath.Join(l.dir, filepath.FromSlash(rel)) }

func (l *local) Close() error { return l.root.Close() }

func localErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission):
		return err
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == "path escapes from parent" {
		return ErrOutside
	}
	return err
}
