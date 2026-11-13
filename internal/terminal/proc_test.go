package terminal

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStatusBusyAndFolder(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	m := NewManager()
	info, err := m.Create(Options{Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	st, err := m.Status(info.ID)
	if err != nil || st.Running || st.Busy {
		t.Fatalf("before start: %+v %v", st, err)
	}
	s, _ := m.get(info.ID)
	if err := s.start(80, 24); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	st, _ = m.Status(info.ID)
	if !st.Running || st.Busy || st.Folder != dir {
		t.Fatalf("idle: %+v (want folder %s)", st, dir)
	}
	if _, err := s.f.Write([]byte("sleep 3\n")); err != nil {
		t.Fatal(err)
	}
	busy := false
	for i := 0; i < 20 && !busy; i++ {
		time.Sleep(100 * time.Millisecond)
		st, _ = m.Status(info.ID)
		busy = st.Busy
	}
	if !busy || st.Program != "sleep" {
		t.Fatalf("running sleep: %+v", st)
	}
	if _, err := m.Status("nope"); err == nil {
		t.Fatal("unknown session: no error")
	}
}
