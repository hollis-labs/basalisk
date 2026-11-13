package files

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// remote is a folder on another host, reached with the system ssh client (so the user's
// ssh config, keys, agent and jump hosts all apply) running the host's sftp subsystem.
type remote struct {
	host string
	dir  string // as configured: absolute, or relative to the remote home ("~/dev", "dev")

	mu     sync.Mutex
	client *sftp.Client
	cmd    *exec.Cmd
	base   string                       // the folder's resolved path on the host, symlinks resolved
	dial   func() (*sftp.Client, error) // tests: an in-process SFTP server instead of ssh
}

func newRemote(host, dir string) *remote { return &remote{host: host, dir: dir} }

// sshPath finds ssh even when the app is started without a login PATH.
func sshPath() string {
	if p, err := exec.LookPath("ssh"); err == nil {
		return p
	}
	return "/usr/bin/ssh"
}

func (r *remote) connectLocked() error {
	if r.client != nil {
		return nil
	}
	if r.dial != nil { // tests: an in-process SFTP server
		c, err := r.dial()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRemote, err)
		}
		return r.bindLocked(c, nil)
	}
	// The host is from the config and cannot start with "-" (config.go refuses it).
	cmd := exec.Command(sshPath(), //nolint:gosec // fixed argv; the host is checked config
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-s", r.host, "sftp")
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("%w: %w", ErrRemote, err)
	}
	type result struct {
		c   *sftp.Client
		err error
	}
	done := make(chan result, 1)
	go func() {
		cl, cerr := sftp.NewClientPipe(out, in)
		done <- result{cl, cerr}
	}()
	var res result
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		res.err = errors.New("timed out")
	}
	if res.err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("%w: %w", ErrRemote, res.err)
	}
	return r.bindLocked(res.c, cmd)
}

// bindLocked resolves the folder on the connected host and keeps the connection.
func (r *remote) bindLocked(c *sftp.Client, cmd *exec.Cmd) error {
	dir := r.dir
	if dir == "~" || dir == "" {
		dir = "."
	} else if strings.HasPrefix(dir, "~/") {
		dir = dir[2:]
	}
	base, err := c.RealPath(dir)
	if err == nil {
		var fi fs.FileInfo
		if fi, err = c.Stat(base); err == nil && !fi.IsDir() {
			err = ErrNotDir
		}
	}
	if err != nil {
		_ = c.Close()
		if cmd != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		return fmt.Errorf("%w: %s: %w", ErrRemote, r.dir, err)
	}
	r.client, r.cmd, r.base = c, cmd, path.Clean(base)
	return nil
}

func (r *remote) dropLocked() {
	if r.client != nil {
		_ = r.client.Close()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	r.client, r.cmd = nil, nil
}

// do runs fn with a connected client, reconnecting once if the connection was lost.
func (r *remote) do(fn func(c *sftp.Client, base string) error) error {
	for attempt := 0; ; attempt++ {
		r.mu.Lock()
		err := r.connectLocked()
		c, base := r.client, r.base
		r.mu.Unlock()
		if err != nil {
			return err
		}
		err = fn(c, base)
		if !lost(err) || attempt > 0 {
			return err
		}
		r.mu.Lock()
		if r.client == c {
			r.dropLocked()
		}
		r.mu.Unlock()
	}
}

func lost(err error) bool {
	return err != nil && (errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed) ||
		strings.Contains(err.Error(), "connection lost"))
}

func inside(base, p string) bool { return p == base || strings.HasPrefix(p, base+"/") || base == "/" }

func abs(base, rel string) string {
	if rel == "" {
		return base
	}
	return base + "/" + rel
}

// check resolves rel on the host and refuses it when it leads outside the root. For a
// path that does not exist yet, its folder is checked instead.
func check(c *sftp.Client, base, rel string, mayBeMissing bool) error {
	p := abs(base, rel)
	resolved, err := c.RealPath(p)
	if err == nil {
		if _, serr := c.Lstat(p); serr != nil {
			err = serr
		}
	}
	if err != nil {
		if !mayBeMissing || !errors.Is(err, fs.ErrNotExist) || rel == "" {
			return remoteErr(err)
		}
		resolved, err = c.RealPath(path.Dir(p))
		if err != nil {
			return remoteErr(err)
		}
	}
	if !inside(base, path.Clean(resolved)) {
		return ErrOutside
	}
	return nil
}

func (r *remote) stat(c *sftp.Client, base, rel string) (Entry, error) {
	p := abs(base, rel)
	fi, err := c.Lstat(p)
	if err != nil {
		return Entry{}, remoteErr(err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return entryFrom(rel, fi), nil
	}
	e := entryFrom(rel, fi)
	e.Symlink = true
	resolved, err := c.RealPath(p)
	if err != nil || !inside(base, path.Clean(resolved)) {
		e.Broken, e.Size = true, 0
		return e, nil //nolint:nilerr // a dangling or escaping link is listed, marked broken
	}
	t, err := c.Stat(p)
	if err != nil {
		e.Broken, e.Size = true, 0
		return e, nil //nolint:nilerr // as above
	}
	e2 := entryFrom(rel, t)
	e2.Symlink = true
	return e2, nil
}

func (r *remote) Stat(_ context.Context, rel string) (Entry, error) {
	var e Entry
	err := r.do(func(c *sftp.Client, base string) error {
		if rel != "" {
			parent := path.Dir(rel)
			if parent == "." {
				parent = ""
			}
			if err := check(c, base, parent, false); err != nil {
				return err
			}
		}
		var err error
		e, err = r.stat(c, base, rel)
		return err
	})
	return e, err
}

func (r *remote) List(_ context.Context, rel string) ([]Entry, error) {
	var out []Entry
	err := r.do(func(c *sftp.Client, base string) error {
		if err := check(c, base, rel, false); err != nil {
			return err
		}
		resolved, _ := c.RealPath(abs(base, rel))
		fis, err := c.ReadDir(resolved)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return ErrNotFound
			}
			if fi, serr := c.Stat(resolved); serr == nil && !fi.IsDir() {
				return ErrNotDir
			}
			return remoteErr(err)
		}
		out = make([]Entry, 0, len(fis))
		for _, fi := range fis {
			child := join(rel, fi.Name())
			if fi.Mode()&fs.ModeSymlink != 0 {
				e, err := r.stat(c, base, child)
				if err != nil {
					continue
				}
				out = append(out, e)
				continue
			}
			out = append(out, entryFrom(child, fi))
		}
		return nil
	})
	return out, err
}

func (r *remote) Open(_ context.Context, rel string) (Content, Entry, error) {
	var (
		f *sftp.File
		e Entry
	)
	err := r.do(func(c *sftp.Client, base string) error {
		if err := check(c, base, rel, false); err != nil {
			return err
		}
		var err error
		if e, err = r.stat(c, base, rel); err != nil {
			return err
		}
		if e.Broken {
			return ErrNotFound
		}
		if e.Dir {
			return ErrIsDir
		}
		f, err = c.Open(abs(base, rel))
		return remoteErr(err)
	})
	if err != nil {
		return nil, Entry{}, err
	}
	return f, e, nil
}

func (r *remote) Write(_ context.Context, rel string, body io.Reader, ifMatch string) (Entry, error) {
	var e Entry
	// The body can be read only once, so a lost connection is not retried mid-write.
	used := false
	err := r.do(func(c *sftp.Client, base string) error {
		if used {
			return ErrRemote
		}
		if err := check(c, base, rel, true); err != nil {
			return err
		}
		cur, err := r.stat(c, base, rel)
		exists := err == nil
		switch {
		case err != nil && !errors.Is(err, ErrNotFound):
			return err
		case exists && cur.Dir:
			return ErrIsDir
		case ifMatch != "" && (!exists || cur.ETag() != ifMatch):
			return ErrConflict
		}
		used = true
		target := abs(base, rel)
		if exists && cur.Symlink {
			if target, err = c.RealPath(target); err != nil {
				return remoteErr(err)
			}
		}
		perm := fs.FileMode(0o644)
		if exists {
			if fi, serr := c.Stat(target); serr == nil {
				perm = fi.Mode().Perm()
			}
		}
		tmp := path.Join(path.Dir(target), "."+path.Base(target)+".basalisk-"+rand.Text()[:8])
		f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return remoteErr(err)
		}
		if _, err = io.Copy(f, body); err == nil {
			err = f.Close()
		} else {
			_ = f.Close()
		}
		if err == nil {
			err = c.Chmod(tmp, perm)
		}
		if err == nil {
			err = c.PosixRename(tmp, target)
		}
		if err != nil {
			_ = c.Remove(tmp)
			return remoteErr(err)
		}
		e, err = r.stat(c, base, rel)
		return err
	})
	return e, err
}

func (r *remote) LocalPath(string) string { return "" }

func (r *remote) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropLocked()
	return nil
}

func remoteErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case lost(err):
		return fmt.Errorf("%w: %w", ErrRemote, err)
	}
	return err
}
