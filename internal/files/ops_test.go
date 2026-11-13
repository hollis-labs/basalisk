package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// opsFixture: a local place ("dev", under a temp home with its own Trash) and a remote one
// ("rem") served by an in-process SFTP server over a pipe from another temp folder.
func opsFixture(t *testing.T) (m *Manager, home, remoteDir string) {
	t.Helper()
	home = t.TempDir()
	must(t, os.MkdirAll(filepath.Join(home, "dev"), 0o750))
	remoteDir = t.TempDir()
	m, err := New(Config{Local: []RootConfig{{ID: "dev", Label: "dev", Path: "~/dev"}}}, home)
	must(t, err)
	rem := newRemote("stub", "~")
	rem.dial = func() (*sftp.Client, error) {
		cr, sw := io.Pipe()
		sr, cw := io.Pipe()
		srv, err := sftp.NewServer(struct {
			io.Reader
			io.WriteCloser
		}{sr, sw}, sftp.WithServerWorkingDirectory(remoteDir))
		if err != nil {
			return nil, err
		}
		// When the client closes, the server stops; close its end too so the client's reader
		// sees the end (a real ssh connection ends when the process is killed).
		go func() {
			_ = srv.Serve()
			_ = sw.Close()
		}()
		return sftp.NewClientPipe(cr, cw)
	}
	m.roots = append(m.roots, Root{ID: "rem", Label: "rem", Kind: "sftp", Host: "stub", Path: "~"})
	m.by["rem"] = rem
	t.Cleanup(m.Close)
	return m, home, remoteDir
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o750))
	must(t, os.WriteFile(p, []byte(body), 0o600))
}

func TestOpsBothPlaces(t *testing.T) {
	m, home, remoteDir := opsFixture(t)
	ctx := context.Background()
	dirs := map[string]string{"dev": filepath.Join(home, "dev"), "rem": remoteDir}
	for _, root := range []string{"dev", "rem"} {
		t.Run(root, func(t *testing.T) {
			disk := dirs[root]
			writeFile(t, filepath.Join(disk, "a.md"), "alpha")
			writeFile(t, filepath.Join(disk, "dir/inner.txt"), "inner")
			writeFile(t, filepath.Join(disk, "node_modules/pkg/target.js"), "x")

			// Create: a folder and an empty file; a taken name is refused, never overwritten.
			if _, err := m.Mkdir(ctx, root, "new"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Mkdir(ctx, root, "new"); !errors.Is(err, ErrExists) {
				t.Fatalf("mkdir twice: %v", err)
			}
			if _, err := m.Create(ctx, root, "new/empty.txt"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Create(ctx, root, "a.md"); !errors.Is(err, ErrExists) {
				t.Fatalf("create over a file: %v", err)
			}
			if b, _ := os.ReadFile(filepath.Join(disk, "a.md")); string(b) != "alpha" { //nolint:gosec // test temp dir
				t.Fatal("a.md changed")
			}
			for _, bad := range []string{"../escape", "new/../../escape", "", "new/"} {
				if _, err := m.Mkdir(ctx, root, bad); err == nil {
					t.Errorf("mkdir %q accepted", bad)
				}
			}

			// Rename: refused onto a taken name or out of the root.
			if _, err := m.Move(ctx, Ref{root, "a.md"}, Ref{root, "dir/inner.txt"}); !errors.Is(err, ErrExists) {
				t.Fatalf("rename onto a file: %v", err)
			}
			if _, err := m.Move(ctx, Ref{root, "a.md"}, Ref{root, "../a.md"}); !errors.Is(err, ErrOutside) {
				t.Fatalf("rename out: %v", err)
			}
			if _, err := m.Move(ctx, Ref{root, "dir"}, Ref{root, "dir/sub"}); err == nil {
				t.Fatal("moved a folder into itself")
			}
			if _, err := m.Move(ctx, Ref{root, "a.md"}, Ref{root, "b.md"}); err != nil {
				t.Fatal(err)
			}

			// Copy: a file and a folder with its contents; never over a taken name.
			if _, err := m.Copy(ctx, Ref{root, "b.md"}, Ref{root, "b copy.md"}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Copy(ctx, Ref{root, "dir"}, Ref{root, "dir copy"}); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(disk, "dir copy/inner.txt")); string(b) != "inner" { //nolint:gosec // test temp dir
				t.Fatalf("copied folder: %q", b)
			}
			if _, err := m.Copy(ctx, Ref{root, "b.md"}, Ref{root, "dir"}); !errors.Is(err, ErrExists) {
				t.Fatalf("copy onto a folder: %v", err)
			}

			// Search: by name, case-insensitive, node_modules skipped.
			res, err := m.Search(ctx, root, "", "INNER")
			if err != nil || len(res.Entries) != 2 {
				t.Fatalf("search inner: %+v %v", res, err)
			}
			if res, _ = m.Search(ctx, root, "", "target"); len(res.Entries) != 0 {
				t.Fatalf("searched node_modules: %+v", res)
			}
			if res, _ = m.Search(ctx, root, "node_modules", "target"); len(res.Entries) != 1 {
				t.Fatalf("search inside node_modules: %+v", res)
			}

			// Delete: local to the Trash, remote for good.
			trashed, err := m.Delete(ctx, root, "b copy.md")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(disk, "b copy.md")); !os.IsNotExist(err) {
				t.Fatal("still there")
			}
			if root == "dev" {
				if !trashed {
					t.Fatal("local delete did not trash")
				}
				if b, _ := os.ReadFile(filepath.Join(home, ".Trash", "b copy.md")); string(b) != "alpha" { //nolint:gosec // test temp dir
					t.Fatalf("trash: %q", b)
				}
			} else if trashed {
				t.Fatal("remote delete claimed a Trash")
			}
			if _, err = m.Delete(ctx, root, "../x"); err == nil {
				t.Fatal("deleted outside the root")
			}
		})
	}

	// Across places: a folder moves from local to remote; the original goes to the Trash.
	if _, err := m.Move(ctx, Ref{"dev", "dir copy"}, Ref{"rem", "moved"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(remoteDir, "moved/inner.txt")); string(b) != "inner" { //nolint:gosec // test temp dir
		t.Fatalf("moved: %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, "dev", "dir copy")); !os.IsNotExist(err) {
		t.Fatal("source kept after a move")
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash", "dir copy")); err != nil {
		t.Fatal("source not in the Trash")
	}
	// And a file copies back, refusing a clash.
	if _, err := m.Copy(ctx, Ref{"rem", "b.md"}, Ref{"dev", "b.md"}); !errors.Is(err, ErrExists) {
		t.Fatalf("cross copy clash: %v", err)
	}
	if _, err := m.Copy(ctx, Ref{"rem", "b.md"}, Ref{"dev", "from-remote.md"}); err != nil {
		t.Fatal(err)
	}
}

// A link that leads out of the root is never followed by copy, and delete moves the link,
// not what it points at.
func TestOpsLinksStayInside(t *testing.T) {
	m, home, _ := opsFixture(t)
	ctx := context.Background()
	secret := filepath.Join(home, "secret.txt")
	writeFile(t, secret, "no")
	must(t, os.Symlink(secret, filepath.Join(home, "dev", "out")))
	if _, err := m.Copy(ctx, Ref{"dev", "out"}, Ref{"dev", "out copy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "dev", "out copy")); !os.IsNotExist(err) {
		t.Fatal("copied through a link that leaves the root")
	}
	if _, err := m.Delete(ctx, "dev", "out"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(secret); string(b) != "no" { //nolint:gosec // test temp dir
		t.Fatal("the link's target was touched")
	}
}

func TestPlacesAddRemove(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "files.json")
	m, err := New(Config{Local: []RootConfig{}}, home)
	must(t, err)
	defer m.Close()
	m.SetConfigFile(cfg)
	r, err := m.AddRemote(RootConfig{Host: "box", Path: "~/notes"})
	must(t, err)
	if r.ID != "box-notes" || r.Kind != "sftp" {
		t.Fatal(r)
	}
	if _, err = m.AddRemote(RootConfig{Host: "box", Path: "~/notes"}); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	for _, bad := range []RootConfig{{Host: "-oProxyCommand=x", Path: "/"}, {Host: "box", Path: "relative"}, {Host: "a b", Path: "/"}} {
		if _, err = m.AddRemote(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	c, err := LoadConfig(cfg, "")
	must(t, err)
	if len(c.Remotes) != 1 || c.Remotes[0].Host != "box" {
		t.Fatalf("saved: %+v", c)
	}
	if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
	must(t, m.RemoveRoot("box-notes"))
	if c, _ = LoadConfig(cfg, ""); len(c.Remotes) != 0 || len(m.Roots()) != 0 {
		t.Fatalf("after remove: %+v %+v", c, m.Roots())
	}
}

func TestSSHHosts(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".ssh", "config"), "Host box other\n  User me\nHost *.lan !bad\nhost third\nMatch all\n")
	if got := strings.Join(SSHHosts(home), ","); got != "box,other,third" {
		t.Fatal(got)
	}
}

func TestGrepScanAndQuoting(t *testing.T) {
	m, home, _ := opsFixture(t)
	dev := filepath.Join(home, "dev")
	writeFile(t, filepath.Join(dev, "notes/plan.md"), "first line\nThe Brackets idea\nlast")
	writeFile(t, filepath.Join(dev, "node_modules/x/index.js"), "brackets inside deps")
	writeFile(t, filepath.Join(dev, "bin.dat"), "brackets\x00binary")
	res, err := m.Grep(context.Background(), "dev", "", "BRACKETS")
	must(t, err)
	if len(res.Matches) != 1 || res.Matches[0].Path != "notes/plan.md" || res.Matches[0].Line != 2 || res.Matches[0].Text != "The Brackets idea" {
		t.Fatalf("%+v", res)
	}
	if _, err = m.Grep(context.Background(), "dev", "../", "x"); !errors.Is(err, ErrOutside) {
		t.Fatal(err)
	}
	if got := shQuote(`it's; rm -rf ~`); got != `'it'\''s; rm -rf ~'` {
		t.Fatal(got)
	}
	matches, cut := readRG(strings.NewReader(`{"type":"match","data":{"path":{"text":"/base/a.md"},"lines":{"text":"hit\n"},"line_number":3}}
{"type":"match","data":{"path":{"text":"/elsewhere/b.md"},"lines":{"text":"no"},"line_number":1}}
{"type":"summary"}`), "/base")
	if cut || len(matches) != 1 || matches[0] != (Match{Path: "a.md", Line: 3, Text: "hit"}) {
		t.Fatalf("%+v", matches)
	}
}

// A read-only content search on a real host, when BASALISK_LIVE_SFTP names one ("host:path").
func TestLiveRemoteGrep(t *testing.T) {
	spec := os.Getenv("BASALISK_LIVE_SFTP")
	if spec == "" {
		t.Skip("set BASALISK_LIVE_SFTP=host:path for a live check")
	}
	host, dir, _ := strings.Cut(spec, ":")
	m, err := New(Config{Local: []RootConfig{}, Remotes: []RootConfig{{ID: "live", Host: host, Path: dir}}}, t.TempDir())
	must(t, err)
	defer m.Close()
	res, err := m.Grep(context.Background(), "live", "", "package")
	must(t, err)
	t.Logf("engine=%s matches=%d truncated=%v first=%+v", res.Engine, len(res.Matches), res.Truncated, res.Matches[:min(1, len(res.Matches))])
	if res.Engine != "ripgrep" {
		t.Fatal(res.Engine)
	}
	names, err := m.Search(context.Background(), "live", "", "readme")
	must(t, err)
	t.Logf("name search: %d entries, truncated=%v", len(names.Entries), names.Truncated)
}
