package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config chooses the roots. The zero value serves the default local folders and no remote.
//
// As a file (files.json beside the database, or BASALISK_FILES_CONFIG):
//
//	{"local": [{"id": "home", "label": "Home", "path": "~"}],
//	 "remotes": [{"id": "box-dev", "label": "box ~/dev", "host": "box", "path": "~/dev"}]}
//
// A missing "local" keeps the defaults; an empty list serves no local folder.
type Config struct {
	Local   []RootConfig `json:"local,omitempty"`
	Remotes []RootConfig `json:"remotes,omitempty"`
}

// RootConfig is one configured root. Host is an ssh destination (a Host alias from the
// ssh config works); Path is absolute or starts with "~".
type RootConfig struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Host  string `json:"host,omitempty"`
	Path  string `json:"path"`
}

// DefaultLocal are the local folders served when the config names none; those that do not
// exist are skipped.
var DefaultLocal = []RootConfig{
	{ID: "home", Label: "Home", Path: "~"},
	{ID: "dev", Label: "dev", Path: "~/dev"},
	{ID: "downloads", Label: "Downloads", Path: "~/Downloads"},
	{ID: "documents", Label: "Documents", Path: "~/Documents"},
	{ID: "projects", Label: "Projects", Path: "~/Projects"},
}

// LoadConfig reads a config file; a missing file is the zero Config. Remotes given in
// env (BASALISK_FILES_REMOTES: "host:path" items, comma separated) are added.
func LoadConfig(file, env string) (Config, error) {
	var c Config
	if file != "" {
		b, err := os.ReadFile(file) //nolint:gosec // the path is operator configuration
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return c, err
		default:
			if err = json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("%s: %w", file, err)
			}
		}
	}
	for _, item := range strings.Split(env, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		host, p, ok := strings.Cut(item, ":")
		if !ok || host == "" || p == "" {
			return c, fmt.Errorf("BASALISK_FILES_REMOTES: %q is not host:path", item)
		}
		c.Remotes = append(c.Remotes, RootConfig{Host: host, Path: p})
	}
	return c, nil
}

var idUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(idUnsafe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// New opens the roots. home is this machine's home directory.
func New(c Config, home string) (*Manager, error) {
	m := &Manager{by: map[string]backend{}, cfg: c, home: home}
	add := func(r Root, b backend) error {
		if r.ID == "" || slug(r.ID) != r.ID {
			return fmt.Errorf("files: root id %q must be lower-case letters, digits and dashes", r.ID)
		}
		if _, dup := m.by[r.ID]; dup {
			return fmt.Errorf("files: two roots are called %q", r.ID)
		}
		m.roots = append(m.roots, r)
		m.by[r.ID] = b
		return nil
	}
	locals := c.Local
	if locals == nil {
		locals = DefaultLocal
	}
	for _, rc := range locals {
		dir := rc.Path
		if dir == "~" {
			dir = home
		} else if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
		if !filepath.IsAbs(dir) {
			m.Close()
			return nil, fmt.Errorf("files: local root %q: path must be absolute or start with ~", rc.ID)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		l, err := openLocal(dir)
		if err != nil {
			continue
		}
		l.trash = filepath.Join(home, ".Trash")
		label := rc.Label
		if label == "" {
			label = filepath.Base(dir)
		}
		if err = add(Root{ID: rc.ID, Label: label, Kind: "local", Path: dir}, l); err != nil {
			_ = l.Close()
			m.Close()
			return nil, err
		}
	}
	for _, rc := range c.Remotes {
		if rc.Host == "" || rc.Path == "" || strings.HasPrefix(rc.Host, "-") {
			m.Close()
			return nil, fmt.Errorf("files: remote root %q needs a host and a path", rc.ID)
		}
		id, label := rc.ID, rc.Label
		if id == "" {
			id = slug(rc.Host + "-" + strings.TrimPrefix(rc.Path, "~"))
		}
		if label == "" {
			label = rc.Host + ":" + rc.Path
		}
		if err := add(Root{ID: id, Label: label, Kind: "sftp", Host: rc.Host, Path: rc.Path}, newRemote(rc.Host, rc.Path)); err != nil {
			m.Close()
			return nil, err
		}
	}
	return m, nil
}
