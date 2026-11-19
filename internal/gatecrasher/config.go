// Package gatecrasher shows the owner the approvals that wait on them in Gatecrasher, the
// portfolio's approval service, and sends their decisions back.
//
// Basalisk follows Gatecrasher's event stream as the owner (GET /api/gate/v1/events),
// keeps each approval's card (GET /api/gate/v1/approvals/{id}/card) and passes the owner's
// decision on (POST …/decide). The cards themselves are Gatecrasher's: Basalisk keeps them
// as they come and the page draws them.
//
// A request_secret card's value never passes through here. The page posts it from the
// webview straight to the card's intake URL; this package has no route, field or store
// that takes it, and its decide refuses any body field it does not know.
package gatecrasher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config is where Gatecrasher is and the owner's credential for it.
type Config struct {
	// URL is Gatecrasher's base URL (https://…, or http:// for a local test instance).
	URL string `json:"url"`
	// KeyFile holds the owner's Gatecrasher credential (a bearer key). "~/" is the home
	// directory. Empty: owner.key in Basalisk's secrets directory.
	KeyFile string `json:"key_file,omitempty"`
}

// ErrNotConfigured is returned by LoadConfig when there is no config file: approvals are off.
var ErrNotConfigured = errors.New("gatecrasher: not configured")

// LoadConfig reads the config file (gatecrasher.json). defaultKey is the key file used when
// the file names none. A missing file is ErrNotConfigured.
func LoadConfig(path, defaultKey string) (Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the owner's own config file
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotConfigured
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("gatecrasher: %s: %w", path, err)
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	u, err := url.Parse(c.URL)
	if c.URL == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Config{}, fmt.Errorf("gatecrasher: %s: url must be an http(s) URL", path)
	}
	if c.KeyFile == "" {
		c.KeyFile = defaultKey
	}
	if strings.HasPrefix(c.KeyFile, "~/") {
		home, _ := os.UserHomeDir()
		c.KeyFile = filepath.Join(home, c.KeyFile[2:])
	}
	return c, nil
}
