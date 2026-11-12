package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// xdg points every XDG root at a fresh temp tree and returns the base.
func xdg(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	return base
}

func TestDefaultsFollowXDGAndCreateNothing(t *testing.T) {
	base := xdg(t)
	d, err := Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "data", "basalisk"); d.Data != want {
		t.Fatalf("data = %q, want %q", d.Data, want)
	}
	for _, dir := range []string{"data", "config", "state", "cache"} {
		if _, err := os.Stat(filepath.Join(base, dir)); err == nil {
			t.Fatalf("Resolve created %s", dir)
		}
	}
}

func TestExplicitDirectoryWins(t *testing.T) {
	xdg(t)
	data := t.TempDir()
	d, err := Resolve(env(map[string]string{DataDirEnv: data}))
	if err != nil || d.Data != data {
		t.Fatalf("got %+v, %v", d, err)
	}
	if d.Database() != filepath.Join(data, "basalisk.db") || d.SigningKey() != filepath.Join(data, "identity-signing.key") || d.Secrets() != filepath.Join(data, "secrets") {
		t.Fatalf("file names changed: %q %q %q", d.Database(), d.SigningKey(), d.Secrets())
	}
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestPrepareMakesTheDirectoryPrivateAndCreatesNoFile(t *testing.T) {
	d := Dirs{Data: filepath.Join(t.TempDir(), "data")}
	if err := d.Prepare(); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, d.Data); m != 0o700 {
		t.Fatalf("data dir mode = %v", m)
	}
	entries, err := os.ReadDir(d.Data)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Prepare created files: %v, %v", entries, err)
	}
	// A directory left too open by an earlier run is tightened, not trusted.
	if err := os.Chmod(d.Data, 0o755); err != nil { //nolint:gosec // deliberately loose, to prove Prepare tightens it
		t.Fatal(err)
	}
	if err := d.Prepare(); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, d.Data); m != 0o700 {
		t.Fatalf("loose data dir mode = %v", m)
	}
}

func TestPrivateFileCreatesAt0600AndTightens(t *testing.T) {
	// A permissive umask must not widen the file: the mode is 0600 whatever it is.
	p := filepath.Join(t.TempDir(), "x.db")
	if err := PrivateFile(p); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Fatalf("new file mode = %v", m)
	}
	if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // deliberately loose, to prove PrivateFile tightens it
		t.Fatal(err)
	}
	if err := PrivateFile(p); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Fatalf("tightened file mode = %v", m)
	}
	if err := PrivateFile(filepath.Join(t.TempDir(), "missing", "x.db")); err == nil {
		t.Fatal("a file in a directory that does not exist must fail, not create the directory")
	}
}

func TestWithinAndEnsureParentStayInsideTheDataDirectory(t *testing.T) {
	root := t.TempDir()
	d := Dirs{Data: filepath.Join(root, "data")}
	if err := d.Prepare(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		d.Data:                                   true,
		filepath.Join(d.Data, "a", "b"):          true,
		filepath.Join(root, "data-evil", "x"):    false,
		filepath.Join(root, "x"):                 false,
		filepath.Join(d.Data, "..", "x"):         false,
		filepath.Join(d.Data, "..", "data", "y"): true,
	} {
		if got := d.Within(p); got != want {
			t.Errorf("Within(%q) = %v, want %v", p, got, want)
		}
	}
	if err := d.EnsureParent(filepath.Join(d.Data, "db", "deep", "main.db")); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"db", filepath.Join("db", "deep")} {
		if m := mode(t, filepath.Join(d.Data, sub)); m != 0o700 {
			t.Errorf("%s mode = %v, want 0700", sub, m)
		}
	}
	outside := filepath.Join(root, "elsewhere", "x.db")
	if err := d.EnsureParent(outside); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(outside)); err == nil {
		t.Fatal("EnsureParent created a directory outside the data directory")
	}
}
