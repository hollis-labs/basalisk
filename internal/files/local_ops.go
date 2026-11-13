package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (l *local) exists(rel string) bool {
	_, err := l.root.Lstat(rel)
	return err == nil
}

func (l *local) Mkdir(_ context.Context, rel string) (Entry, error) {
	if l.exists(rel) {
		return Entry{}, ErrExists
	}
	if err := l.root.Mkdir(rel, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, ErrExists
		}
		return Entry{}, localErr(err)
	}
	return l.stat(rel)
}

func (l *local) CreateFrom(_ context.Context, rel string, r io.Reader) (Entry, error) {
	f, err := l.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, ErrExists
		}
		return Entry{}, localErr(err)
	}
	if _, err = io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = l.root.Remove(rel)
		return Entry{}, err
	}
	if err = f.Close(); err != nil {
		return Entry{}, err
	}
	return l.stat(rel)
}

func (l *local) Rename(_ context.Context, from, to string) (Entry, error) {
	if _, err := l.root.Lstat(from); err != nil {
		return Entry{}, localErr(err)
	}
	// A case-only rename ("a.md" to "A.md") is the same file on macOS's default volume.
	if l.exists(to) && !strings.EqualFold(from, to) {
		return Entry{}, ErrExists
	}
	if err := l.root.Rename(from, to); err != nil {
		return Entry{}, localErr(err)
	}
	return l.stat(to)
}

// Delete moves rel to the owner's Trash, renamed if the Trash already has that name. Only an
// item whose real folder is inside the root is moved; a link is moved itself, not its target.
func (l *local) Delete(_ context.Context, rel string) (bool, error) {
	if _, err := l.root.Lstat(rel); err != nil {
		return false, localErr(err)
	}
	if l.trash == "" {
		return false, errors.New("no Trash for this place")
	}
	src := filepath.Join(l.dir, filepath.FromSlash(rel))
	dirReal, err1 := filepath.EvalSymlinks(filepath.Dir(src))
	rootReal, err2 := filepath.EvalSymlinks(l.dir)
	if err1 != nil || err2 != nil || (dirReal != rootReal && !strings.HasPrefix(dirReal, rootReal+string(filepath.Separator))) {
		return false, ErrOutside
	}
	if err := os.MkdirAll(l.trash, 0o700); err != nil {
		return false, err
	}
	name := path.Base(rel)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	dest := filepath.Join(l.trash, name)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if i > 999 {
			return false, ErrExists
		}
		dest = filepath.Join(l.trash, fmt.Sprintf("%s %d%s", base, i, ext))
	}
	if err := os.Rename(filepath.Join(dirReal, name), dest); err != nil {
		return false, fmt.Errorf("could not move it to the Trash: %w", err)
	}
	return true, nil
}

func (l *local) Walk(ctx context.Context, rel string, fn func(Entry) bool) error {
	return fs.WalkDir(l.root.FS(), name(rel), func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil || p == name(rel) {
			return nil //nolint:nilerr // an unreadable folder is skipped, not fatal to a search
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr // removed while walking
		}
		e := entryFrom(p, info)
		e.Symlink = d.Type()&fs.ModeSymlink != 0
		if !fn(e) && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
}
