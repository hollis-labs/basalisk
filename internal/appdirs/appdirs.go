// Package appdirs decides where Basalisk keeps its files and makes them private. Durable
// data (the database, the signing key and the secrets store) lives under XDG_DATA_HOME,
// resolved by the shared apppaths layer, or under BASALISK_DATA_DIR when the operator sets it.
package appdirs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
)

const (
	appName = "basalisk"
	// DataDirEnv overrides the whole data directory.
	DataDirEnv = "BASALISK_DATA_DIR"

	// DatabaseFile, SigningKeyFile and SecretsDir are the names inside the data directory.
	DatabaseFile   = "basalisk.db"
	SigningKeyFile = "identity-signing.key"
	SecretsDir     = "secrets"
)

// Dirs are the resolved directories.
type Dirs struct {
	Data string
}

// Resolve returns the data directory. getenv is os.Getenv outside tests. XDG_DATA_HOME is
// honored through the shared apppaths layer. Nothing is created.
func Resolve(getenv func(string) string) (Dirs, error) {
	if v := getenv(DataDirEnv); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return Dirs{}, fmt.Errorf("resolve %s: %w", DataDirEnv, err)
		}
		return Dirs{Data: abs}, nil
	}
	layout, err := paths.Resolve(appName, paths.WithoutMaterialize())
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve application directories: %w", err)
	}
	return Dirs{Data: layout.DataDir()}, nil
}

// Database is the SQLite file.
func (d Dirs) Database() string { return filepath.Join(d.Data, DatabaseFile) }

// SigningKey is the identity module's Ed25519 key file.
func (d Dirs) SigningKey() string { return filepath.Join(d.Data, SigningKeyFile) }

// Secrets is the managed file store for integration credentials.
func (d Dirs) Secrets() string { return filepath.Join(d.Data, SecretsDir) }

// Prepare creates the data directory (0700) when missing and tightens it when it exists, so
// private data is never readable by other users. It creates no file: the database file, the
// signing key and the secrets store are made private by whoever uses them, at the path the
// configuration finally names (see PrivateFile and EnsureParent).
func (d Dirs) Prepare() error {
	if err := os.MkdirAll(d.Data, 0o700); err != nil {
		return err
	}
	return os.Chmod(d.Data, 0o700) //nolint:gosec // 0700 on a directory is the point: owner-only
}

// Within reports whether path is the data directory or below it (lexically).
func (d Dirs) Within(path string) bool {
	rel, err := filepath.Rel(d.Data, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// EnsureParent creates the directories above path (mode 0700, before the umask) when path is
// inside the data directory. Outside it nothing is created: an absolute path elsewhere is the
// operator's, and its directory must already exist.
func (d Dirs) EnsureParent(path string) error {
	if !d.Within(path) {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o700)
}

// PrivateFile makes sure the file at path exists with mode 0600 and tightens an existing one.
// A new file is created with 0600 (never wider, the umask only narrows it), so it is not
// readable by others even for an instant. SQLite gives its -wal, -shm and -journal files the
// mode of the database file, so a 0600 database has 0600 sidecars.
func PrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is operator configuration
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
