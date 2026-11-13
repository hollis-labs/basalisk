package files

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
)

func remoteExists(c *sftp.Client, p string) bool {
	_, err := c.Lstat(p)
	return err == nil
}

func (r *remote) Mkdir(_ context.Context, rel string) (Entry, error) {
	var e Entry
	err := r.do(func(c *sftp.Client, base string) error {
		if err := check(c, base, rel, true); err != nil {
			return err
		}
		p := abs(base, rel)
		if remoteExists(c, p) {
			return ErrExists
		}
		if err := c.Mkdir(p); err != nil {
			return remoteErr(err)
		}
		var err error
		e, err = r.stat(c, base, rel)
		return err
	})
	return e, err
}

func (r *remote) CreateFrom(_ context.Context, rel string, src io.Reader) (Entry, error) {
	var e Entry
	used := false
	err := r.do(func(c *sftp.Client, base string) error {
		if used {
			return ErrRemote // the reader can be read once
		}
		if err := check(c, base, rel, true); err != nil {
			return err
		}
		p := abs(base, rel)
		f, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			if remoteExists(c, p) {
				return ErrExists
			}
			return remoteErr(err)
		}
		used = true
		if _, err = io.Copy(f, src); err != nil {
			_ = f.Close()
			_ = c.Remove(p)
			return remoteErr(err)
		}
		if err = f.Close(); err != nil {
			return remoteErr(err)
		}
		e, err = r.stat(c, base, rel)
		return err
	})
	return e, err
}

func (r *remote) Rename(_ context.Context, from, to string) (Entry, error) {
	var e Entry
	err := r.do(func(c *sftp.Client, base string) error {
		if err := check(c, base, from, false); err != nil {
			return err
		}
		if err := check(c, base, to, true); err != nil {
			return err
		}
		if remoteExists(c, abs(base, to)) {
			return ErrExists
		}
		// Plain SFTP rename refuses an existing target, unlike posix-rename.
		if err := c.Rename(abs(base, from), abs(base, to)); err != nil {
			return remoteErr(err)
		}
		var err error
		e, err = r.stat(c, base, to)
		return err
	})
	return e, err
}

// Delete removes rel for good: a remote host has no Trash to go to.
func (r *remote) Delete(_ context.Context, rel string) (bool, error) {
	err := r.do(func(c *sftp.Client, base string) error {
		p := abs(base, rel)
		fi, err := c.Lstat(p)
		if err != nil {
			return remoteErr(err)
		}
		// The parent must be inside the root; the item itself may be a link, which is removed.
		parent := path.Dir(rel)
		if parent == "." {
			parent = ""
		}
		if err = check(c, base, parent, false); err != nil {
			return err
		}
		if fi.IsDir() {
			return remoteErr(c.RemoveAll(p))
		}
		return remoteErr(c.Remove(p))
	})
	return false, err
}

func (r *remote) Walk(ctx context.Context, rel string, fn func(Entry) bool) error {
	return r.do(func(c *sftp.Client, base string) error {
		if err := check(c, base, rel, false); err != nil {
			return err
		}
		start := abs(base, rel)
		w := c.Walk(start)
		for w.Step() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if w.Err() != nil || w.Path() == start {
				continue
			}
			child := strings.TrimPrefix(strings.TrimPrefix(w.Path(), base), "/")
			fi := w.Stat()
			e := entryFrom(child, fi)
			e.Symlink = fi.Mode()&fs.ModeSymlink != 0
			if !fn(e) && fi.IsDir() {
				w.SkipDir()
			}
		}
		return nil
	})
}
