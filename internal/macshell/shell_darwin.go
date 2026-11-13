//go:build darwin && desktop

package macshell

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework ApplicationServices -framework WebKit
#include <stdlib.h>
#include "shell_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Shell is the macOS implementation of Controller. There is one per process.
type Shell struct {
	mu       sync.Mutex // serializes changes; never held while calling into the main thread from it
	attached bool
	block    bool
	swallow  bool
	combos   []string
	fixMin   bool
	log      eventLog
	native   chan nativeEvent
}

type nativeEvent struct {
	kind int
	code uint32
	mods uint64
}

var shell = &Shell{native: make(chan nativeEvent, 256), combos: []string{}}

// Default is the native shell of this process.
func Default() Controller { return shell }

// Attach gives the shell the NSWindow it drives and installs the observers and hot keys.
// Call it once the window exists; calls before that report "no window".
func Attach(nsWindow unsafe.Pointer) {
	shell.mu.Lock()
	first := !shell.attached
	shell.attached = true
	shell.mu.Unlock()
	C.bslInit(nsWindow)
	attachBrowser(nsWindow) // browser_darwin.go
	if first {
		go shell.pump()
		shell.log.add("attached", "native shell ready")
	}
}

// OnEvent registers a function called for every log entry (from a shell goroutine).
func OnEvent(fn func(Event)) {
	shell.log.mu.Lock()
	shell.log.hook = fn
	shell.log.mu.Unlock()
}

//export bslGoEvent
func bslGoEvent(kind C.int, code C.uint32_t, mods C.uint64_t) {
	// Called on the main thread or the tap thread: hand off and return.
	select {
	case shell.native <- nativeEvent{int(kind), uint32(code), uint64(mods)}:
	default:
	}
}

// pump handles native events on a goroutine, so handlers may call back into the main thread.
func (s *Shell) pump() {
	for ev := range s.native {
		switch ev.kind {
		case C.BSL_EV_ESCAPE:
			s.log.add("escape", "escape chord: leaving kiosk, key swallowing off")
			s.mu.Lock()
			s.swallow = false
			s.block = false
			s.mu.Unlock()
			C.bslSetSwallow(0)
			C.bslSetKiosk(0, 0)
		case C.BSL_EV_TOGGLE_KIOSK:
			on := C.bslKiosk() == 0
			s.log.add("hotkey", fmt.Sprintf("%s: kiosk %s", ToggleKioskChord, onOff(on)))
			s.mu.Lock()
			block := s.block
			s.mu.Unlock()
			if err := s.SetKiosk(on, on && block); err != nil {
				s.log.add("error", err.Error())
			}
		case C.BSL_EV_TOGGLE_MENUBAR:
			if C.bslKiosk() == 0 {
				s.log.add("hotkey", ToggleMenuBarChord+": ignored outside kiosk")
				continue
			}
			visible := C.bslMenuBarVisible() == 0
			s.log.add("hotkey", fmt.Sprintf("%s: menu bar %s", ToggleMenuBarChord, onOff(visible)))
			if err := s.SetMenuBar(visible); err != nil {
				s.log.add("error", err.Error())
			}
		case C.BSL_EV_SWALLOWED:
			s.log.add("swallowed", combo{code: ev.code, mods: ev.mods & modMask}.String())
		case C.BSL_EV_ACTIVE:
			s.log.add("active", map[bool]string{true: "app became frontmost", false: "app lost frontmost"}[ev.code == 1])
		case C.BSL_EV_FIXED_MINIMIZED:
			s.log.add("fix-minimized", fmt.Sprintf("restored a window of pid %d on activation", ev.code))
		case C.BSL_EV_TAP_DISABLED:
			why := map[uint32]string{1: "timeout", 2: "user input"}[ev.code]
			s.log.add("tap", "the system disabled the event tap ("+why+"); re-enabled")
		case C.BSL_EV_WATCHDOG:
			s.mu.Lock()
			s.swallow = false
			s.mu.Unlock()
			s.log.add("watchdog", "main thread unresponsive: key swallowing suspended")
		}
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func (s *Shell) ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.attached {
		return errors.New("the desktop window is not ready")
	}
	return nil
}

// Status implements Controller.
func (s *Shell) Status() Status {
	s.mu.Lock()
	st := Status{
		Available:      s.attached,
		BlockSwitching: s.block,
		Swallow:        s.swallow,
		Combos:         append([]string{}, s.combos...),
		FixMinimized:   s.fixMin,
		Chords:         Chords{Escape: EscapeChord, ToggleKiosk: ToggleKioskChord, ToggleMenuBar: ToggleMenuBarChord},
	}
	attached := s.attached
	s.mu.Unlock()
	st.LastSeq = s.log.last()
	st.Permissions = Permissions{
		Accessibility:   C.bslAXTrusted(0) == 1,
		InputMonitoring: C.bslListenAccess(0) == 1,
	}
	st.TapInstalled = C.bslTapInstalled() == 1
	st.TapLevel = map[C.int]string{1: "hid", 2: "session"}[C.bslTapLevel()]
	st.Active = C.bslIsActive() == 1
	if id := C.bslIdentityJSON(); id != nil {
		_ = json.Unmarshal([]byte(C.GoString(id)), &st.Identity)
		C.free(unsafe.Pointer(id))
	}
	if !attached {
		st.MenuBarVisible = true
		return st
	}
	st.Kiosk = C.bslKiosk() == 1
	st.MenuBarVisible = C.bslMenuBarVisible() == 1
	st.BlockSwitching = st.BlockSwitching && st.Kiosk
	var w, h, top C.double
	var n C.int
	C.bslScreenInfo(&w, &h, &top, &n)
	st.Screen = Screen{Width: float64(w), Height: float64(h), SafeTop: float64(top), Count: int(n)}
	return st
}

// SetKiosk implements Controller.
func (s *Shell) SetKiosk(on, blockSwitching bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if C.bslSetKiosk(cbool(on), cbool(on && blockSwitching)) != 0 {
		return errors.New("AppKit refused the presentation options")
	}
	s.mu.Lock()
	s.block = on && blockSwitching
	s.mu.Unlock()
	s.log.add("kiosk", fmt.Sprintf("kiosk %s, process switching %s", onOff(on), map[bool]string{true: "blocked", false: "allowed"}[on && blockSwitching]))
	return nil
}

// SetMenuBar implements Controller.
func (s *Shell) SetMenuBar(visible bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if C.bslKiosk() == 0 {
		return errors.New("the menu bar is only hidden in kiosk mode")
	}
	if C.bslSetMenuBarVisible(cbool(visible)) != 0 {
		return errors.New("AppKit refused the presentation options")
	}
	s.log.add("menubar", "menu bar "+map[bool]string{true: "shown", false: "hidden"}[visible])
	return nil
}

// SetSwallow implements Controller. Turning swallowing on installs the event tap, which
// needs the Accessibility permission; the escape chord is always handled first.
func (s *Shell) SetSwallow(on bool, combos []string) error {
	if err := s.ready(); err != nil {
		return err
	}
	parsed, names, err := normalizeCombos(combos)
	if err != nil {
		return err
	}
	if len(parsed) > 64 {
		return errors.New("at most 64 shortcuts can be swallowed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		if C.bslTapStart() != 0 {
			return errors.New("the event tap could not be created: grant Basalisk the Accessibility permission (System Settings, Privacy & Security), then try again")
		}
	}
	codes := make([]C.uint32_t, len(parsed)+1)
	mods := make([]C.uint64_t, len(parsed)+1)
	for i, c := range parsed {
		codes[i] = C.uint32_t(c.code)
		mods[i] = C.uint64_t(c.mods)
	}
	C.bslSetCombos(&codes[0], &mods[0], C.int(len(parsed)))
	C.bslSetSwallow(cbool(on))
	if !on {
		C.bslTapStop()
	}
	s.swallow = on
	s.combos = names
	s.log.add("swallow", fmt.Sprintf("key swallowing %s: %v", onOff(on), names))
	return nil
}

// Windows implements Controller.
func (s *Shell) Windows() ([]Window, error) {
	raw := C.bslWindowsJSON()
	if raw == nil {
		return nil, errors.New("listing windows needs the Accessibility permission")
	}
	defer C.free(unsafe.Pointer(raw))
	out := []Window{}
	if err := json.Unmarshal([]byte(C.GoString(raw)), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Unminimize implements Controller.
func (s *Shell) Unminimize(pid, index int) error {
	switch C.bslUnminimize(C.int(pid), C.int(index)) {
	case 0:
		s.log.add("unminimize", fmt.Sprintf("restored window %d of pid %d", index, pid))
		return nil
	case -1:
		return errors.New("restoring a window needs the Accessibility permission")
	case -2:
		return errors.New("no such window")
	default:
		return errors.New("the app refused to restore the window")
	}
}

// SetFixMinimized implements Controller.
func (s *Shell) SetFixMinimized(on bool) error {
	if on && C.bslAXTrusted(0) != 1 {
		return errors.New("restoring minimized windows needs the Accessibility permission")
	}
	C.bslSetFixMinimized(cbool(on))
	s.mu.Lock()
	s.fixMin = on
	s.mu.Unlock()
	s.log.add("fix-minimized", "restore minimized windows on activation "+onOff(on))
	return nil
}

// RequestPermission implements Controller.
func (s *Shell) RequestPermission(kind string) error {
	var granted bool
	var pane string
	switch kind {
	case "accessibility":
		granted, pane = C.bslAXTrusted(1) == 1, "Privacy_Accessibility"
	case "input-monitoring":
		granted, pane = C.bslListenAccess(1) == 1, "Privacy_ListenEvent"
	default:
		return fmt.Errorf("unknown permission %q", kind)
	}
	if !granted {
		anchor := C.CString(pane)
		defer C.free(unsafe.Pointer(anchor))
		C.bslOpenPrivacyPane(anchor)
	}
	s.log.add("permission", "requested "+kind)
	return nil
}

// Events implements Controller.
func (s *Shell) Events(after int64) []Event { return s.log.after(after) }

// Snapshot implements Controller.
func (s *Shell) Snapshot() ([]byte, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var buf *C.uchar
	n := C.bslSnapshotPNG(&buf)
	if n < 0 || buf == nil {
		return nil, errors.New("the page could not be captured")
	}
	defer C.free(unsafe.Pointer(buf))
	return C.GoBytes(unsafe.Pointer(buf), C.int(n)), nil
}

// Eval implements Controller.
func (s *Shell) Eval(js string) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	src := C.CString(js)
	defer C.free(unsafe.Pointer(src))
	raw := C.bslEval(src)
	if raw == nil {
		return nil, errors.New("the page could not be reached")
	}
	defer C.free(unsafe.Pointer(raw))
	return json.RawMessage(C.GoString(raw)), nil
}
