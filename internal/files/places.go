package files

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Remote places added and removed from the Files sidebar, kept in the config file
// (files.json) so they survive a restart. Local places are not changed this way.

// SetConfigFile names the file AddRemote and RemoveRoot keep the remotes in.
func (m *Manager) SetConfigFile(path string) {
	m.mu.Lock()
	m.cfgFile = path
	m.mu.Unlock()
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:-]{0,252}$`)

// AddRemote adds an SFTP place: a host as the ssh config knows it, and a folder there.
func (m *Manager) AddRemote(rc RootConfig) (Root, error) {
	rc.Host, rc.Path, rc.Label = strings.TrimSpace(rc.Host), strings.TrimSpace(rc.Path), strings.TrimSpace(rc.Label)
	if !hostPattern.MatchString(rc.Host) {
		return Root{}, errors.New("a host is a name from the ssh config (letters, digits, . _ - @ :)")
	}
	if rc.Path == "" || (!strings.HasPrefix(rc.Path, "/") && rc.Path != "~" && !strings.HasPrefix(rc.Path, "~/")) || strings.ContainsRune(rc.Path, 0) || len(rc.Path) > 1024 {
		return Root{}, errors.New("a folder is an absolute path or starts with ~")
	}
	if len(rc.Label) > 100 {
		return Root{}, errors.New("the name is too long")
	}
	id := slug(rc.Host + "-" + strings.TrimPrefix(rc.Path, "~"))
	if rc.Label == "" {
		rc.Label = rc.Host + ":" + rc.Path
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.by[id]; dup {
		return Root{}, ErrExists
	}
	rc.ID = id
	r := Root{ID: id, Label: rc.Label, Kind: "sftp", Host: rc.Host, Path: rc.Path}
	m.roots = append(m.roots, r)
	m.by[id] = newRemote(rc.Host, rc.Path)
	m.cfg.Remotes = append(m.cfg.Remotes, rc)
	if err := m.saveLocked(); err != nil {
		m.removeLocked(id)
		return Root{}, err
	}
	return r, nil
}

// RemoveRoot removes a remote place (its files are untouched).
func (m *Manager) RemoveRoot(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(m.roots, func(r Root) bool { return r.ID == id })
	if i < 0 {
		return ErrNoRoot
	}
	if m.roots[i].Kind != "sftp" {
		return errors.New("only remote places can be removed here")
	}
	m.removeLocked(id)
	return m.saveLocked()
}

func (m *Manager) removeLocked(id string) {
	if b, ok := m.by[id]; ok {
		_ = b.Close()
		delete(m.by, id)
	}
	m.roots = slices.DeleteFunc(m.roots, func(r Root) bool { return r.ID == id })
	m.cfg.Remotes = slices.DeleteFunc(m.cfg.Remotes, func(rc RootConfig) bool {
		cid := rc.ID
		if cid == "" {
			cid = slug(rc.Host + "-" + strings.TrimPrefix(rc.Path, "~"))
		}
		return cid == id
	})
}

func (m *Manager) saveLocked() error {
	if m.cfgFile == "" {
		return nil // nowhere to keep it: the change lasts until a restart
	}
	raw, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.cfgFile + ".tmp"
	if err = os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err = os.Rename(tmp, m.cfgFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("save %s: %w", filepath.Base(m.cfgFile), err)
	}
	return nil
}

// SSHHosts lists the concrete Host names in the owner's ~/.ssh/config (no patterns), for
// choosing a remote place.
func SSHHosts(home string) []string {
	f, err := os.Open(filepath.Join(home, ".ssh", "config")) //nolint:gosec // the owner's own ssh config
	if err != nil {
		return []string{}
	}
	defer func() { _ = f.Close() }()
	seen := map[string]bool{}
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, h := range fields[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] || !hostPattern.MatchString(h) {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
