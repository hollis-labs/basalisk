package terminal

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Status is what a running shell is doing: whether a program holds the foreground (the
// terminal's foreground process group is not the shell's), which one, and the folder the
// foreground process is in. Read on demand from the operating system; best effort.
type Status struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Busy    bool   `json:"busy"`
	Program string `json:"program,omitempty"`
	Folder  string `json:"folder,omitempty"`
}

// Status reports a session's foreground program and folder.
func (m *Manager) Status(id string) (Status, error) {
	s, err := m.get(id)
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	f, cmd, started, closed, cwd := s.f, s.cmd, s.started, s.closed, s.info.Cwd
	exited := s.info.Exited
	s.mu.Unlock()
	st := Status{ID: id, Folder: cwd}
	if !started || closed || exited || f == nil || cmd == nil || cmd.Process == nil {
		return st, nil
	}
	st.Running = true
	shell := cmd.Process.Pid
	pgrp := 0
	if rc, err := f.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { pgrp, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) })
	}
	fg := shell
	if pgrp > 0 && pgrp != shell {
		st.Busy, fg = true, pgrp
		st.Program = processName(fg)
	}
	if dir := processDir(fg); dir != "" {
		st.Folder = dir
	}
	return st, nil
}

func processName(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // fixed argv, a pid
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	return name[strings.LastIndex(name, "/")+1:]
}

// processDir is a process's working directory: /proc on Linux, lsof on macOS.
func processDir(pid int) string {
	if runtime.GOOS == "linux" {
		dir, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		return dir
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output() //nolint:gosec // fixed argv, a pid
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n/") {
			return line[1:]
		}
	}
	return ""
}
