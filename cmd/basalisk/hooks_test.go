package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/basalisk/internal/persistence"
)

// bootWith starts every manifest module with the app's boot hooks. mutate may edit the
// generated config first (the tests use it to bind a free loopback port).
func bootWith(ctx context.Context, getenv func(string) string, mutate func(map[string]any)) (*boot.Running, error) {
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(substrateConfigJSON), &cfg); err != nil {
		return nil, err
	}
	if mutate != nil {
		mutate(cfg)
	}
	raw, _ := json.Marshal(cfg)
	fsys, root := persistence.Migrations()
	database.UseMigrationFS(fsys, root)
	return boot.StartWith(ctx, substrateApp, substrateModules, string(raw), bootOptions(getenv))
}

// prepare boots and stops the stack and returns the config the modules ran with, or the
// error that stopped the boot (the legacy-data guard, for one).
func prepare(getenv func(string) string) (string, error) {
	if f := getenv("SUBSTRATE_CONFIG"); f != "" { // the config module reads the process environment
		prev, had := os.LookupEnv("SUBSTRATE_CONFIG")
		_ = os.Setenv("SUBSTRATE_CONFIG", f)
		defer func() {
			if had {
				_ = os.Setenv("SUBSTRATE_CONFIG", prev)
			} else {
				_ = os.Unsetenv("SUBSTRATE_CONFIG")
			}
		}()
	}
	run, err := bootWith(context.Background(), getenv, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = run.Stop(context.Background()) }()
	out, err := json.Marshal(run.App.Config)
	return string(out), err
}
