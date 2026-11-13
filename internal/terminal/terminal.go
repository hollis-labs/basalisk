// Package terminal runs local shells in pseudo-terminals and lets browser terminals attach
// to them. A session outlives its viewers: closing the page or reloading it leaves the
// shell running, and the next attach replays recent output before going live.
//
// The design follows Tachyon's chat terminal (internal/chatterminal): a PTY per session, a
// bounded replay buffer, fan-out to attached viewers. It differs in running a local login
// shell instead of ssh, in keeping no database rows, and in having no idle timeout.
package terminal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// MaxSessions bounds how many shells run at once.
	MaxSessions = 24
	// replayMax is how much recent output a new viewer gets.
	replayMax = 512 << 10
	// subscriberBuffer is how many output chunks may wait for one viewer. A viewer that
	// falls further behind is dropped and reconnects into a replay.
	subscriberBuffer = 512
	readChunk        = 32 << 10
	maxDimension     = 1000
)

// ErrNotFound means the session id is unknown.
var ErrNotFound = errors.New("no such terminal session")

// ErrTooMany means MaxSessions shells are already running.
var ErrTooMany = fmt.Errorf("at most %d terminal sessions can be open", MaxSessions)

// Options are what a new session may choose.
type Options struct {
	// Cwd is the starting directory. Empty means the home directory.
	Cwd string
	// Title is a label for the tab. Empty means the shell's name.
	Title string
}

// Info describes a session.
type Info struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Shell     string    `json:"shell"`
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
	Started   bool      `json:"started"`
	Exited    bool      `json:"exited"`
	ExitCode  int       `json:"exit_code"`
	Cols      uint16    `json:"cols"`
	Rows      uint16    `json:"rows"`
	Viewers   int       `json:"viewers"`
}

// Manager owns the sessions of one process.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// NewManager returns an empty manager.
func NewManager() *Manager { return &Manager{sessions: map[string]*session{}} }

type session struct {
	mu      sync.Mutex
	info    Info
	f       *os.File
	cmd     *exec.Cmd
	replay  []byte
	subs    map[chan []byte]struct{}
	done    chan struct{} // closed when the shell has exited
	started bool
	closed  bool
}

// Create registers a session. The shell starts on the first attach, at that viewer's size.
func (m *Manager) Create(o Options) (Info, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cwd := o.Cwd
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return Info{}, fmt.Errorf("the starting directory %q is not a directory", cwd)
	}
	title := strings.TrimSpace(o.Title)
	if title == "" {
		title = filepath.Base(shell)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live := 0
	for _, s := range m.sessions {
		if !s.exited() {
			live++
		}
	}
	if live >= MaxSessions {
		return Info{}, ErrTooMany
	}
	s := &session{
		info: Info{ID: strings.ToLower(rand.Text()[:12]), Title: title, Shell: shell, Cwd: cwd, CreatedAt: time.Now()},
		subs: map[chan []byte]struct{}{},
		done: make(chan struct{}),
	}
	m.sessions[s.info.ID] = s
	return s.info, nil
}

// List returns the sessions, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Rename sets a session's title.
func (m *Manager) Rename(id, title string) (Info, error) {
	s, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 80 {
		return Info{}, errors.New("a title is 1 to 80 characters")
	}
	s.mu.Lock()
	s.info.Title = title
	s.mu.Unlock()
	return s.snapshot(), nil
}

// Close ends a session and forgets it.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	s.kill()
	return nil
}

// CloseAll ends every session. Call it when the process stops.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := m.sessions
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	for _, s := range all {
		s.kill()
	}
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// Attachment is one viewer of a session.
type Attachment struct {
	s *session
	// Replay is the recent output, to be written before anything from Frames.
	Replay []byte
	// Frames carries live output. It is closed when the shell exits or this viewer fell
	// too far behind; Exited tells which.
	Frames chan []byte
}

// Attach adds a viewer, starting the shell if this is the first one.
func (m *Manager) Attach(id string, cols, rows uint16) (*Attachment, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	cols, rows = clamp(cols, 100), clamp(rows, 30)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrNotFound
	}
	if !s.started {
		if err := s.start(cols, rows); err != nil {
			return nil, err
		}
	}
	a := &Attachment{s: s, Replay: append([]byte(nil), s.replay...), Frames: make(chan []byte, subscriberBuffer)}
	if s.exitedLocked() {
		close(a.Frames)
		return a, nil
	}
	s.subs[a.Frames] = struct{}{}
	return a, nil
}

// Detach removes the viewer. The shell keeps running.
func (a *Attachment) Detach() {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if _, ok := a.s.subs[a.Frames]; ok {
		delete(a.s.subs, a.Frames)
		close(a.Frames)
	}
}

// Input writes keystrokes to the shell.
func (a *Attachment) Input(p []byte) error {
	a.s.mu.Lock()
	f := a.s.f
	a.s.mu.Unlock()
	if f == nil {
		return errors.New("the shell is not running")
	}
	_, err := f.Write(p)
	return err
}

// Resize sets the terminal size. Sizes outside 1..maxDimension are ignored.
func (a *Attachment) Resize(cols, rows uint16) {
	if cols == 0 || rows == 0 || cols > maxDimension || rows > maxDimension {
		return
	}
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if a.s.f == nil {
		return
	}
	if pty.Setsize(a.s.f, &pty.Winsize{Cols: cols, Rows: rows}) == nil {
		a.s.info.Cols, a.s.info.Rows = cols, rows
	}
}

// Exited reports whether the shell has ended, and its exit code.
func (a *Attachment) Exited() (bool, int) {
	select {
	case <-a.s.done:
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		return true, a.s.info.ExitCode
	default:
		return false, 0
	}
}

func clamp(v, def uint16) uint16 {
	if v == 0 || v > maxDimension {
		return def
	}
	return v
}

// start runs the login shell in a PTY. The caller holds s.mu.
func (s *session) start(cols, rows uint16) error {
	// A login shell, so the profile sets PATH and the rest even when the app was started
	// from the Finder with a bare environment.
	cmd := exec.Command(s.info.Shell, "-l")
	cmd.Dir = s.info.Cwd
	cmd.Env = shellEnv(os.Environ())
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return fmt.Errorf("start %s: %w", s.info.Shell, err)
	}
	s.f, s.cmd, s.started = f, cmd, true
	s.info.Started, s.info.Cols, s.info.Rows = true, cols, rows
	go s.pump()
	return nil
}

// shellEnv is the parent environment with the terminal's own variables set.
func shellEnv(parent []string) []string {
	set := map[string]string{
		"TERM":         "xterm-256color",
		"COLORTERM":    "truecolor",
		"TERM_PROGRAM": "Basalisk",
	}
	hasLang := false
	out := make([]string, 0, len(parent)+len(set)+1)
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; replaced {
			continue
		}
		if k == "LANG" || k == "LC_ALL" {
			hasLang = true
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	if !hasLang {
		out = append(out, "LANG=en_US.UTF-8")
	}
	return out
}

// pump copies shell output into the replay buffer and to the viewers until the shell ends.
func (s *session) pump() {
	buf := make([]byte, readChunk)
	for {
		n, err := s.f.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			s.replay = appendReplay(s.replay, chunk)
			for ch := range s.subs {
				select {
				case ch <- chunk:
				default:
					// Too slow: drop the viewer. It reconnects and gets the replay.
					delete(s.subs, ch)
					close(ch)
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code := 0
	if err := s.cmd.Wait(); err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	s.mu.Lock()
	_ = s.f.Close()
	s.f = nil
	s.info.Exited, s.info.ExitCode = true, code
	close(s.done)
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
}

// appendReplay keeps the newest replayMax bytes, cut at a UTF-8 character boundary so the
// replay does not begin in the middle of a character.
func appendReplay(replay, chunk []byte) []byte {
	replay = append(replay, chunk...)
	if len(replay) <= replayMax {
		return replay
	}
	cut := len(replay) - replayMax
	for cut < len(replay) && replay[cut]&0xC0 == 0x80 {
		cut++
	}
	return append(replay[:0:0], replay[cut:]...)
}

func (s *session) exitedLocked() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) exited() bool { return s.exitedLocked() }

func (s *session) snapshot() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	info.Viewers = len(s.subs)
	return info
}

// kill ends the shell and everything in its session: hang up first, as closing a terminal
// window does, then kill what ignored it.
func (s *session) kill() {
	s.mu.Lock()
	s.closed = true
	cmd, started := s.cmd, s.started
	s.mu.Unlock()
	if !started || cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	select {
	case <-s.done:
		return
	case <-time.After(500 * time.Millisecond):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
