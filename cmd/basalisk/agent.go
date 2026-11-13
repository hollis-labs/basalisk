package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/basalisk/internal/agent"
	"github.com/hollis-labs/basalisk/internal/api"
)

// enableAgent starts the AI core (internal/agent): its conversations in a file beside the
// database, its model at the gateway the config names. With no gateway configured the
// routes are still served, and say so. A store that cannot be opened is logged and leaves
// the agent off; the rest of the app does not depend on it.
//
// The environment can stand in for the config, for a development run:
// BASALISK_AGENT_GATEWAY, BASALISK_AGENT_KEY_FILE, BASALISK_AGENT_MODEL.
func enableAgent(ctx context.Context, a *api.API, cfg agent.Config, dbPath string) {
	if v := os.Getenv("BASALISK_AGENT_GATEWAY"); v != "" {
		cfg.GatewayURL = v
	}
	if v := os.Getenv("BASALISK_AGENT_KEY_FILE"); v != "" {
		cfg.KeyFile = v
	}
	if v := os.Getenv("BASALISK_AGENT_MODEL"); v != "" {
		cfg.Model = v
	}
	home, _ := os.UserHomeDir()
	if cfg.KeyFile == "" && home != "" {
		cfg.KeyFile = filepath.Join(home, ".config", "basalisk", "agent-key")
	}
	if rest, ok := strings.CutPrefix(cfg.KeyFile, "~/"); ok && home != "" {
		cfg.KeyFile = filepath.Join(home, rest)
	}
	store, err := agent.OpenStore(ctx, dbPath)
	if err != nil {
		slog.Warn("the agent is off: its store could not be opened", "path", dbPath, "err", err)
		return
	}
	go func() {
		<-ctx.Done()
		_ = store.Close()
	}()
	a.EnableAgent(agent.New(ctx, cfg, store, agent.NewGateway(cfg)))
	slog.Info("agent enabled", "ready", cfg.Ready(), "model", cfg.Model)
}
