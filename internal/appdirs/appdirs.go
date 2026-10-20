// Package appdirs decides where Basalisk keeps its files. Durable data (the
// database and the secrets store) lives under XDG_DATA_HOME and operator
// configuration under XDG_CONFIG_HOME, resolved by the shared apppaths layer.
// Nothing here creates a directory; the data store makes its own.
package appdirs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
)

const (
	appName = "basalisk"
	// DataDirEnv overrides the whole data directory.
	DataDirEnv = "BASALISK_DATA_DIR"
	// ConfigDirEnv overrides the whole configuration directory.
	ConfigDirEnv = "BASALISK_CONFIG_DIR"
	// AssuranceConfigEnv names the assurance host configuration file.
	AssuranceConfigEnv = "BASALISK_ASSURANCE_CONFIG"

	assuranceConfigFile = "assurance.json"
	databaseFile        = "basalisk.db"
)

// ErrLegacyData means a database exists where earlier versions kept it
// (XDG_STATE_HOME) but not where this version looks for it. Opening the new,
// empty location would silently start a second installation and reopen
// first-run owner setup, so startup stops instead.
var ErrLegacyData = errors.New("basalisk data found in the legacy state directory")

// Dirs are the resolved directories.
type Dirs struct {
	Data   string
	Config string
	// legacy is the pre-XDG-data location, used only to detect unmigrated data.
	legacy string
}

// Resolve returns the data and configuration directories. getenv is os.Getenv
// outside tests. XDG_*_HOME is honored through the shared apppaths layer.
func Resolve(getenv func(string) string) (Dirs, error) {
	layout, err := paths.Resolve(appName, paths.WithoutMaterialize())
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve application directories: %w", err)
	}
	d := Dirs{Data: layout.DataDir(), Config: layout.ConfigDir(), legacy: layout.StateDir()}
	if v := getenv(ConfigDirEnv); v != "" {
		d.Config = v
	}
	if v := getenv(DataDirEnv); v != "" {
		// An explicit data directory is the operator's choice; do not second-guess it.
		d.Data, d.legacy = v, ""
		return d, nil
	}
	if d.legacy != "" && exists(filepath.Join(d.legacy, databaseFile)) && !exists(filepath.Join(d.Data, databaseFile)) {
		return d, fmt.Errorf("%w: move %s to %s, or set %s to keep using it",
			ErrLegacyData, d.legacy, d.Data, DataDirEnv)
	}
	return d, nil
}

// AssuranceConfigPath is the assurance host configuration file: the explicit
// environment setting if present, otherwise assurance.json in the config
// directory when that file exists, otherwise empty (assurance not configured).
func (d Dirs) AssuranceConfigPath(getenv func(string) string) string {
	if v := getenv(AssuranceConfigEnv); v != "" {
		return v
	}
	if p := filepath.Join(d.Config, assuranceConfigFile); exists(p) {
		return p
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
