package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host box-1 box-2\n  User x\nHost *.example\nHost *\n"), 0o600)
	file := filepath.Join(home, "commands.json")
	_ = os.WriteFile(file, []byte(`{"commands":[
	 {"name":"say","about":"echo","argv":["echo","{word}"],"where":"local","run":"outright","params":{"word":{"about":"a word","pattern":"[a-z]+"}}},
	 {"name":"touchy","about":"changes, guarded","argv":["touch","{file}"],"where":"local","changes":true,"run":"outright","params":{"file":{"about":"a file","pattern":"/[A-Za-z0-9_./-]+"}},"guards":[{"about":"the marker is there","argv":["test","-e","{file}.ok"]}]},
	 {"name":"asks","about":"asks","argv":["true"],"where":"local","run":"ask"},
	 {"name":"host-memory","off":true},
	 {"name":"Bad Name","argv":["true"],"where":"local","run":"outright"}]}`), 0o600)
	l := List{File: file, Home: home}
	ctx := context.Background()
	all, problem := l.All()
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if problem == nil || !names["say"] || names["host-memory"] || names["Bad Name"] || !names["git-status"] {
		t.Fatalf("the owner's file adds, removes and is checked: %v %v", problem, names)
	}
	if h := l.Hosts(); strings.Join(h, ",") != "box-1,box-2" {
		t.Fatalf("hosts are the named ones in the ssh config: %v", h)
	}
	r, err := l.Run(ctx, "say", "", map[string]string{"word": "hello"}, false)
	if err != nil || strings.TrimSpace(r.Output) != "hello" || r.Exit != 0 {
		t.Fatalf("%v %+v", err, r)
	}
	// A value outside the pattern is refused before anything runs; there is no shell to break out of.
	for _, bad := range []string{"hello; rm -rf /", "$(id)", "a b", ""} {
		if _, err := l.Run(ctx, "say", "", map[string]string{"word": bad}, false); err == nil {
			t.Fatalf("%q was let through", bad)
		}
	}
	if _, err := l.Run(ctx, "say", "", map[string]string{"word": "x", "extra": "y"}, false); err == nil {
		t.Fatal("an unknown parameter is refused")
	}
	if _, err := l.Run(ctx, "asks", "", nil, false); err == nil {
		t.Fatal("a command marked ask does not run without the owner")
	}
	if _, err := l.Run(ctx, "asks", "", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Run(ctx, "say", "box-9", map[string]string{"word": "x"}, false); err == nil {
		t.Fatal("a local command does not run on a host")
	}
	if _, err := l.Run(ctx, "host-uptime", "evil.example", nil, false); err == nil {
		t.Fatal("a host not in the ssh config is refused")
	}
	target := filepath.Join(home, "made")
	if _, err := l.Run(ctx, "touchy", "", map[string]string{"file": target}, false); err == nil {
		t.Fatal("a changing command does not run when its guard fails")
	}
	_ = os.WriteFile(target+".ok", nil, 0o600)
	if _, err := l.Run(ctx, "touchy", "", map[string]string{"file": target}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("with its guard passing it runs")
	}
	s, err := l.Run(ctx, "secret-generate", "", map[string]string{"name": "relay-key"}, false)
	if err != nil || strings.Contains(s.Output, "=") && len(s.Output) > 200 {
		t.Fatalf("%v %+v", err, s)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "basalisk", "secrets", "relay-key"))
	if len(strings.TrimSpace(string(data))) < 40 || strings.Contains(s.Output, strings.TrimSpace(string(data))) {
		t.Fatal("the secret is in its file and not in what is returned")
	}
	if _, err := l.Run(ctx, "secret-generate", "", map[string]string{"name": "relay-key"}, false); err == nil {
		t.Fatal("an existing secret is not replaced")
	}
	if q := quote([]string{"a b", "it's"}); q != `'a b' 'it'\''s'` {
		t.Fatalf("quoting for a remote shell: %s", q)
	}
}
