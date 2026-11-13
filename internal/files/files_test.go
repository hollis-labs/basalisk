package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture: home/{dev/a.md, dev/sub/, dev/in -> a.md, dev/out -> ../secret, secret}
func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	dev := filepath.Join(home, "dev")
	must(t, os.MkdirAll(filepath.Join(dev, "sub"), 0o750))
	must(t, os.WriteFile(filepath.Join(dev, "a.md"), []byte("# hi\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(home, "secret"), []byte("no"), 0o600))
	must(t, os.Symlink("a.md", filepath.Join(dev, "in")))
	must(t, os.Symlink("../secret", filepath.Join(dev, "out")))
	m, err := New(Config{}, home)
	must(t, err)
	t.Cleanup(m.Close)
	return m, home
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRootsSkipMissing(t *testing.T) {
	m, home := fixture(t)
	var ids []string
	for _, r := range m.Roots() {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "home,dev" {
		t.Fatalf("roots = %s", got)
	}
	if m.Roots()[1].Path != filepath.Join(home, "dev") || m.Roots()[1].Kind != "local" {
		t.Fatalf("dev root = %+v", m.Roots()[1])
	}
}

func TestList(t *testing.T) {
	m, _ := fixture(t)
	list, err := m.List(context.Background(), "dev", "")
	must(t, err)
	var got []string
	for _, e := range list {
		s := e.Path
		if e.Dir {
			s += "/"
		}
		if e.Symlink {
			s += "@"
		}
		if e.Broken {
			s += "!"
		}
		got = append(got, s)
	}
	if strings.Join(got, " ") != "sub/ a.md in@ out@!" {
		t.Fatalf("list = %v", got)
	}
	if _, err = m.List(context.Background(), "dev", "a.md"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("list of a file: %v", err)
	}
}

func TestContainment(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	for _, p := range []string{"..", "../secret", "sub/../../secret", "/../secret"} {
		if _, err := m.Stat(ctx, "dev", p); !errors.Is(err, ErrOutside) {
			t.Errorf("stat %q: %v, want ErrOutside", p, err)
		}
	}
	if _, _, err := m.Open(ctx, "dev", "out"); err == nil {
		t.Fatal("opened a link that leads outside the root")
	}
	f, e, err := m.Open(ctx, "dev", "in")
	must(t, err)
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "# hi\n" || !e.Symlink {
		t.Fatalf("in = %q %+v", b, e)
	}
	// The home root does reach it.
	if _, err = m.Stat(ctx, "home", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Stat(ctx, "nope", ""); !errors.Is(err, ErrNoRoot) {
		t.Fatal(err)
	}
}

func TestWrite(t *testing.T) {
	m, home := fixture(t)
	ctx := context.Background()
	e, err := m.Stat(ctx, "dev", "a.md")
	must(t, err)
	if _, err = m.Write(ctx, "dev", "a.md", strings.NewReader("x"), `"stale"`); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	e2, err := m.Write(ctx, "dev", "a.md", strings.NewReader("# new\n"), e.ETag())
	must(t, err)
	b, _ := os.ReadFile(filepath.Join(home, "dev", "a.md")) //nolint:gosec // test temp dir
	fi, _ := os.Stat(filepath.Join(home, "dev", "a.md"))
	if string(b) != "# new\n" || e2.Size != 6 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("after write: %q %+v %v", b, e2, fi.Mode())
	}
	// Through a link, the link stays.
	_, err = m.Write(ctx, "dev", "in", strings.NewReader("via link"), "")
	must(t, err)
	if fi, _ := os.Lstat(filepath.Join(home, "dev", "in")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link replaced by a file")
	}
	if _, err = m.Write(ctx, "dev", "out", strings.NewReader("x"), ""); err == nil {
		t.Fatal("wrote through a link that leads outside")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "secret")); string(b) != "no" { //nolint:gosec // test temp dir
		t.Fatal("secret changed")
	}
	// A new file; no temporary files left over.
	_, err = m.Write(ctx, "dev", "sub/new.txt", strings.NewReader("n"), "")
	must(t, err)
	des, _ := os.ReadDir(filepath.Join(home, "dev", "sub"))
	if len(des) != 1 {
		t.Fatalf("sub = %v", des)
	}
	if _, err = m.Write(ctx, "dev", "", strings.NewReader("x"), ""); !errors.Is(err, ErrIsDir) {
		t.Fatal(err)
	}
}

func TestConfig(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"), "box:~/dev, other:/srv")
	must(t, err)
	m, err := New(Config{Local: []RootConfig{}, Remotes: c.Remotes}, t.TempDir())
	must(t, err)
	defer m.Close()
	r := m.Roots()
	if len(r) != 2 || r[0].ID != "box-dev" || r[0].Kind != "sftp" || r[1].ID != "other-srv" {
		t.Fatalf("roots = %+v", r)
	}
	if _, err = LoadConfig("", "nocolon"); err == nil {
		t.Fatal("bad env accepted")
	}
	if _, err = New(Config{Remotes: []RootConfig{{Host: "-oProxyCommand=x", Path: "/"}}}, t.TempDir()); err == nil {
		t.Fatal("option-like host accepted")
	}
}
