package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/agent"
	"github.com/hollis-labs/basalisk/internal/api"
)

// enableAgent starts the AI core (internal/agent): its conversations in a file beside the
// database, its model at the gateway the config names. With no gateway configured the
// routes are still served, and say so. It returns the runtime (nil when the agent is off), so
// the caller can give it tools. A store that cannot be opened is logged and leaves
// the agent off; the rest of the app does not depend on it.
//
// The environment can stand in for the config, for a development run:
// BASALISK_AGENT_GATEWAY, BASALISK_AGENT_KEY_FILE, BASALISK_AGENT_MODEL,
// BASALISK_AGENT_RULES_FILE, BASALISK_AGENT_REFLEXES_FILE.
func enableAgent(ctx context.Context, a *api.API, cfg agent.Config, dbPath string) *agent.Runtime {
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
		return nil
	}
	go func() {
		<-ctx.Done()
		_ = store.Close()
	}()
	rt := agent.New(ctx, cfg, store, agent.NewGateway(cfg))
	// The owner's projects, from a starting list if they have none yet.
	if home != "" {
		if n, err := rt.ImportProjects(ctx, filepath.Join(home, ".config", "basalisk", "projects.json")); err != nil {
			slog.Warn("agent: the starting list of projects was not read", "err", err)
		} else if n > 0 {
			slog.Info("agent: projects read from the starting list", "count", n)
		}
	}
	// The owner's roster (the seats the assistant writes to), from a starting list likewise.
	if home != "" {
		if n, err := rt.ImportRoster(ctx, filepath.Join(home, ".config", "basalisk", "roster.json")); err != nil {
			slog.Warn("agent: the starting roster was not read", "err", err)
		} else if n > 0 {
			slog.Info("agent: roster read from the starting list", "count", n)
		}
	}
	// The owner's standing instructions, from a starting file the first time there are none
	// (a later start never overwrites them; the agent keeps them current with standing_update).
	standing := os.Getenv("BASALISK_AGENT_STANDING_FILE")
	if standing == "" && home != "" {
		standing = filepath.Join(home, ".config", "basalisk", "standing.md")
	}
	if standing != "" {
		if ok, err := rt.ImportStanding(ctx, standing); err != nil {
			slog.Warn("agent: the starting standing instructions were not read", "err", err)
		} else if ok {
			slog.Info("agent: standing instructions read from the starting file")
		}
	}
	// The router's rules: the ones that ship, and the owner's own from this file if it exists.
	rules := os.Getenv("BASALISK_AGENT_RULES_FILE")
	if rules == "" && home != "" {
		rules = filepath.Join(home, ".config", "basalisk", "agent-rules.json")
	}
	rt.SetRouter(agent.NewRouter(rules))
	// The reflexes: the ones that ship, and the owner's own from this file if it exists.
	reflexFile := os.Getenv("BASALISK_AGENT_REFLEXES_FILE")
	if reflexFile == "" && home != "" {
		reflexFile = filepath.Join(home, ".config", "basalisk", "agent-reflexes.json")
	}
	rt.SetReflexes(agent.NewReflexes(reflexFile, store))
	// Interval triggers: the minutes are counted while Basalisk runs.
	go rt.RunTriggers(ctx, time.Minute)
	a.EnableAgent(rt)
	slog.Info("agent enabled", "ready", cfg.Ready(), "model", cfg.Model)
	return rt
}
