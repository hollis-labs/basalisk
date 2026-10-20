package appdirs

import (
	"errors"
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

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
	if want := filepath.Join(base, "config", "basalisk"); d.Config != want {
		t.Fatalf("config = %q, want %q", d.Config, want)
	}
	for _, dir := range []string{"data", "config", "state", "cache"} {
		if _, err := os.Stat(filepath.Join(base, dir)); err == nil {
			t.Fatalf("Resolve created %s", dir)
		}
	}
}

func TestExplicitDirectoriesWin(t *testing.T) {
	xdg(t)
	data, cfg := t.TempDir(), t.TempDir()
	d, err := Resolve(env(map[string]string{DataDirEnv: data, ConfigDirEnv: cfg}))
	if err != nil || d.Data != data || d.Config != cfg {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestLegacyStateDataStopsStartup(t *testing.T) {
	base := xdg(t)
	touch(t, filepath.Join(base, "state", "basalisk", "basalisk.db"))
	_, err := Resolve(env(nil))
	if !errors.Is(err, ErrLegacyData) {
		t.Fatalf("err = %v, want ErrLegacyData", err)
	}
}

func TestLegacyGuardClearsOnceDataIsMovedOrOverridden(t *testing.T) {
	base := xdg(t)
	touch(t, filepath.Join(base, "state", "basalisk", "basalisk.db"))

	if _, err := Resolve(env(map[string]string{DataDirEnv: filepath.Join(base, "state", "basalisk")})); err != nil {
		t.Fatalf("explicit legacy directory refused: %v", err)
	}
	touch(t, filepath.Join(base, "data", "basalisk", "basalisk.db"))
	if _, err := Resolve(env(nil)); err != nil {
		t.Fatalf("migrated data still refused: %v", err)
	}
}

func TestAssuranceConfigPath(t *testing.T) {
	xdg(t)
	cfg := t.TempDir()
	d, err := Resolve(env(map[string]string{ConfigDirEnv: cfg}))
	if err != nil {
		t.Fatal(err)
	}
	none := env(nil)
	if got := d.AssuranceConfigPath(none); got != "" {
		t.Fatalf("no file: got %q", got)
	}
	touch(t, filepath.Join(cfg, "assurance.json"))
	if got, want := d.AssuranceConfigPath(none), filepath.Join(cfg, "assurance.json"); got != want {
		t.Fatalf("config dir file: got %q, want %q", got, want)
	}
	if got := d.AssuranceConfigPath(env(map[string]string{AssuranceConfigEnv: "/explicit.json"})); got != "/explicit.json" {
		t.Fatalf("env must win: got %q", got)
	}
}
