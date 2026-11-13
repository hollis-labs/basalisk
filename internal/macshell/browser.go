package macshell

import "encoding/json"

// Browser drives the native web views the desktop's Browser windows show. The page drives
// the same views directly (window.webkit.messageHandlers.basaliskBrowser); this is the way
// in for Go, which is the agent API's. nil outside the macOS desktop build.
type Browser interface {
	// List describes the open views.
	List() ([]BrowserView, error)
	// Command runs one control message, the same JSON object the page posts (for example
	// {"op":"navigate","id":"w3","url":"https://example.com"}), and returns its result.
	Command(msg map[string]any) (json.RawMessage, error)
	// Snapshot renders one view as a PNG.
	Snapshot(id string) ([]byte, error)
}

// BrowserView is the state of one view.
type BrowserView struct {
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Loading  bool    `json:"loading"`
	Progress float64 `json:"progress"`
	Back     bool    `json:"back"`
	Forward  bool    `json:"forward"`
	Visible  bool    `json:"visible"`
}
