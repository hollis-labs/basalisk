//go:build darwin && desktop

package macshell

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=12.0
#include <stdlib.h>
#include "browser_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

type browser struct{}

// DefaultBrowser is this process's browser views.
func DefaultBrowser() Browser { return browser{} }

// attachBrowser installs the page's control channel on the window's web view.
func attachBrowser(nsWindow unsafe.Pointer) { C.bslBrowserInit(nsWindow) }

func takeJSON(p *C.char) []byte {
	if p == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(p))
	return []byte(C.GoString(p))
}

func (browser) List() ([]BrowserView, error) {
	raw := takeJSON(C.bslBrowserListJSON())
	if raw == nil {
		return []BrowserView{}, nil
	}
	var out []BrowserView
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (browser) Command(msg map[string]any) (json.RawMessage, error) {
	// What an agent does to a page the owner may be signed in to goes in the shell log, where
	// the desktop shows it.
	if op, _ := msg["op"].(string); op != "state" {
		shell.log.add("browser", describeCommand(msg))
	}
	in, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	src := C.CString(string(in))
	defer C.free(unsafe.Pointer(src))
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err = json.Unmarshal(takeJSON(C.bslBrowserCommand(src)), &answer); err != nil {
		return nil, err
	}
	if answer.Error != "" {
		return nil, errors.New(answer.Error)
	}
	return answer.Result, nil
}

func (browser) Snapshot(id string) ([]byte, error) {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	var buf *C.uchar
	n := C.bslBrowserSnapshotPNG(cid, &buf)
	if n < 0 || buf == nil {
		return nil, errors.New("no such view, or the snapshot failed")
	}
	defer C.free(unsafe.Pointer(buf))
	return C.GoBytes(unsafe.Pointer(buf), C.int(n)), nil
}

// describeCommand is a log line for one agent command: the op, the view and its argument.
func describeCommand(msg map[string]any) string {
	op, _ := msg["op"].(string)
	id, _ := msg["id"].(string)
	line := fmt.Sprintf("agent %s %s", op, id)
	switch op {
	case "navigate":
		u, _ := msg["url"].(string)
		line += " " + u
	case "eval":
		js, _ := msg["js"].(string)
		if r := []rune(js); len(r) > 200 {
			js = string(r[:200]) + "…"
		}
		line += ": " + js
	case "zoom":
		line += fmt.Sprintf(" %v", msg["zoom"])
	}
	return line
}
