// Package macshell is the platform adapter for the desktop shell: the small native layer the
// web UI cannot reach. It hides the menu bar and Dock (kiosk presentation), owns keyboard
// shortcuts through an event tap, and reads and restores other apps' windows through the
// Accessibility API.
//
// The implementation is macOS only and is compiled with the "desktop" build tag (cgo and
// Objective-C). Every other build gets Default() == nil, and callers report the shell as
// unavailable. A Linux adapter would implement the same Controller.
package macshell

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Controller is what the rest of the app may ask of the native shell.
type Controller interface {
	// Status is a snapshot of the shell.
	Status() Status
	// SetKiosk makes the window borderless and screen sized and hides the menu bar and Dock.
	// blockSwitching also disables Cmd-Tab while the app is frontmost.
	SetKiosk(on, blockSwitching bool) error
	// SetMenuBar shows or hides the menu bar while in kiosk mode.
	SetMenuBar(visible bool) error
	// SetSwallow turns owning the keyboard on or off. While on and this app is frontmost,
	// every key that carries Command, Control or Option (and every function key) is delivered
	// to this app alone, so no other app's global hot key and no system shortcut fires.
	// combos, written like "cmd+q", are additionally kept from this app itself and reported
	// in the log: the shortcuts that would otherwise quit, hide or minimize it.
	SetSwallow(on bool, combos []string) error
	// Windows lists other apps' windows (needs the Accessibility permission).
	Windows() ([]Window, error)
	// Unminimize restores one window from the Dock and brings its app forward.
	Unminimize(pid, index int) error
	// SetFixMinimized restores a window when an app is activated with all of them minimized.
	SetFixMinimized(on bool) error
	// RequestPermission shows the system prompt for "accessibility" or "input-monitoring".
	RequestPermission(kind string) error
	// Events returns the log entries after the given sequence number.
	Events(after int64) []Event
	// Snapshot renders the page as the window shows it, as a PNG. It needs no screen
	// recording permission, which makes it the way an agent checks the real app.
	Snapshot() ([]byte, error)
	// Eval runs JavaScript in the page and returns {"result": ...} or {"error": "..."}.
	Eval(js string) (json.RawMessage, error)
}

// EscapeChord always leaves kiosk mode and stops key swallowing. It is handled before any
// other key and cannot be swallowed.
const EscapeChord = "ctrl+alt+cmd+escape"

// Global shortcuts the shell registers (Carbon hot keys, no permission needed).
const (
	ToggleKioskChord   = "ctrl+alt+cmd+f"
	ToggleMenuBarChord = "ctrl+alt+cmd+m"
	// SummonChord brings Basalisk forward from any app and opens the agent's panel with the
	// keyboard in it (the same letter as the panel's own shortcut, cmd+j).
	SummonChord = "ctrl+alt+cmd+j"
)

// SummonEvent is the DOM event the page is sent when the summon chord is pressed.
const SummonEvent = "basalisk:summon"

// Status is a snapshot of the shell.
type Status struct {
	Available      bool     `json:"available"`
	Active         bool     `json:"active"` // the app is frontmost
	Kiosk          bool     `json:"kiosk"`
	MenuBarVisible bool     `json:"menu_bar_visible"`
	BlockSwitching bool     `json:"block_switching"`
	Swallow        bool     `json:"swallow"`
	Combos         []string `json:"combos"`
	TapInstalled   bool     `json:"tap_installed"`
	// TapLevel is "hid" when the tap sits ahead of other apps' global hot keys, "session"
	// when it could only be placed behind them (they will still fire), "" with no tap.
	TapLevel     string      `json:"tap_level"`
	FixMinimized bool        `json:"fix_minimized"`
	Permissions  Permissions `json:"permissions"`
	Screen       Screen      `json:"screen"`
	Identity     Identity    `json:"identity"`
	Chords       Chords      `json:"chords"`
	LastSeq      int64       `json:"last_seq"`
}

// Permissions are the privacy grants the native layer depends on.
type Permissions struct {
	Accessibility   bool `json:"accessibility"`
	InputMonitoring bool `json:"input_monitoring"`
}

// Screen describes the display the window is on.
type Screen struct {
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	SafeTop float64 `json:"safe_top"` // height of the camera housing strip, 0 without one
	Count   int     `json:"count"`    // connected displays
}

// Identity is what macOS attaches privacy grants to.
type Identity struct {
	BundleID string `json:"bundle_id"`
	Path     string `json:"path"`
	Bundled  bool   `json:"bundled"`
}

// Chords are the fixed shortcuts.
type Chords struct {
	Escape        string `json:"escape"`
	ToggleKiosk   string `json:"toggle_kiosk"`
	ToggleMenuBar string `json:"toggle_menu_bar"`
}

// Window is one window of another app.
type Window struct {
	PID       int    `json:"pid"`
	App       string `json:"app"`
	BundleID  string `json:"bundle_id"`
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Subrole   string `json:"subrole"`
	Minimized bool   `json:"minimized"`
	AppHidden bool   `json:"app_hidden"`
	AppActive bool   `json:"app_active"`
}

// Event is one log entry: a shortcut that fired, a key that was swallowed, a state change.
type Event struct {
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// eventLog keeps the most recent events.
type eventLog struct {
	mu   sync.Mutex
	seq  int64
	buf  []Event
	hook func(Event)
}

const eventLogSize = 200

func (l *eventLog) add(kind, detail string) {
	l.mu.Lock()
	l.seq++
	e := Event{Seq: l.seq, At: time.Now(), Kind: kind, Detail: detail}
	l.buf = append(l.buf, e)
	if len(l.buf) > eventLogSize {
		l.buf = l.buf[len(l.buf)-eventLogSize:]
	}
	hook := l.hook
	l.mu.Unlock()
	if hook != nil {
		hook(e)
	}
}

func (l *eventLog) after(seq int64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Event{}
	for _, e := range l.buf {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

func (l *eventLog) last() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}

// Modifier bits, equal to the CGEventFlags masks.
const (
	modShift uint64 = 1 << 17
	modCtrl  uint64 = 1 << 18
	modAlt   uint64 = 1 << 19
	modCmd   uint64 = 1 << 20
	modMask         = modShift | modCtrl | modAlt | modCmd
)

// keycodes are macOS virtual key codes. Letter and digit codes are positions on the ANSI
// layout, so "q" means the key where Q is on a US keyboard.
var keycodes = map[string]uint32{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9, "b": 11,
	"q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21,
	"6": 22, "5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29, "]": 30, "o": 31,
	"u": 32, "[": 33, "i": 34, "p": 35, "l": 37, "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42,
	",": 43, "/": 44, "n": 45, "m": 46, ".": 47, "`": 50,
	"return": 36, "tab": 48, "space": 49, "delete": 51, "escape": 53,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
	"f9": 101, "f10": 109, "f11": 103, "f12": 111,
	"left": 123, "right": 124, "down": 125, "up": 126,
}

var keynames = func() map[uint32]string {
	m := make(map[uint32]string, len(keycodes))
	for name, code := range keycodes {
		m[code] = name
	}
	return m
}()

// combo is a parsed shortcut.
type combo struct {
	code uint32
	mods uint64
}

// parseCombo reads "cmd+shift+q". Modifiers: cmd, ctrl, alt (or opt, option), shift.
func parseCombo(s string) (combo, error) {
	var c combo
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return c, fmt.Errorf("shortcut %q has no key", s)
	}
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "cmd", "command", "meta":
			c.mods |= modCmd
		case "ctrl", "control":
			c.mods |= modCtrl
		case "alt", "opt", "option":
			c.mods |= modAlt
		case "shift":
			c.mods |= modShift
		default:
			return c, fmt.Errorf("shortcut %q: unknown modifier %q", s, p)
		}
	}
	code, ok := keycodes[strings.TrimSpace(parts[len(parts)-1])]
	if !ok {
		return c, fmt.Errorf("shortcut %q: unknown key %q", s, parts[len(parts)-1])
	}
	c.code = code
	return c, nil
}

// String is the canonical spelling: modifiers in a fixed order, then the key.
func (c combo) String() string {
	var parts []string
	if c.mods&modCtrl != 0 {
		parts = append(parts, "ctrl")
	}
	if c.mods&modAlt != 0 {
		parts = append(parts, "alt")
	}
	if c.mods&modShift != 0 {
		parts = append(parts, "shift")
	}
	if c.mods&modCmd != 0 {
		parts = append(parts, "cmd")
	}
	name, ok := keynames[c.code]
	if !ok {
		name = fmt.Sprintf("key%d", c.code)
	}
	return strings.Join(append(parts, name), "+")
}

// normalizeCombos parses, de-duplicates and sorts a shortcut list. The escape chord and
// Force Quit (cmd+alt+escape) are refused: neither may ever be swallowed.
func normalizeCombos(in []string) ([]combo, []string, error) {
	seen := map[combo]bool{}
	var out []combo
	for _, s := range in {
		c, err := parseCombo(s)
		if err != nil {
			return nil, nil, err
		}
		if c.code == keycodes["escape"] && c.mods&modCmd != 0 && c.mods&modAlt != 0 {
			return nil, nil, fmt.Errorf("shortcut %q is reserved: the escape chord and Force Quit are never swallowed", s)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.String()
	}
	sort.Strings(names)
	return out, names, nil
}
