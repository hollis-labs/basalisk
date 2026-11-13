package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hollis-labs/basalisk/internal/tetherlink"
)

// The owner's work systems, through the Tether gateway (internal/tetherlink): tasks in
// Torque and knowledge in Tesseract. The gateway offers hundreds of tools; the agent is
// given the few listed here, by name, each with the level this file gives it. A tool that
// is not listed here cannot be called, whatever the gateway offers.
//
// Reading is free. Anything that creates or changes a record asks first.
var tetherTools = map[string]string{
	// Tesseract: what is known.
	"tesseract_recall": LevelRead,
	"tesseract_get":    LevelRead,
	// Torque: what is to be done.
	"torque_project_list":      LevelRead,
	"torque_project_get":       LevelRead,
	"torque_epic_list":         LevelRead,
	"torque_epic_get":          LevelRead,
	"torque_sprint_list":       LevelRead,
	"torque_sprint_get":        LevelRead,
	"torque_task_list":         LevelRead,
	"torque_task_get":          LevelRead,
	"torque_comment_list":      LevelRead,
	"torque_task_subtodo_list": LevelRead,
	"torque_task_create":       LevelWrite,
	"torque_task_update":       LevelWrite,
	"torque_task_transition":   LevelWrite,
	"torque_comment_add":       LevelWrite,
	"torque_task_subtodo_add":  LevelWrite,
	"torque_task_subtodo_done": LevelWrite,
}

// tetherLink is what the source needs of the gateway client.
type tetherLink interface {
	Configured() bool
	Tools(ctx context.Context) ([]tetherlink.ToolInfo, error)
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

// TetherTools is the source of the gateway's tools. The first run after a start asks the
// gateway what it has (in the background: starting the gateway can take seconds, and a
// message does not wait for it); later runs use that list, refreshed now and then.
func TetherTools(link tetherLink) ToolSource {
	var (
		mu       sync.Mutex
		tools    []ServerTool
		loaded   time.Time
		loading  bool
		lastWarn time.Time
	)
	load := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		list, err := link.Tools(ctx)
		mu.Lock()
		defer mu.Unlock()
		loading = false
		if err != nil {
			// Try again in a minute, not on every message; say so once in a while.
			loaded = time.Now().Add(-9 * time.Minute)
			if time.Since(lastWarn) > 10*time.Minute {
				lastWarn = time.Now()
				slog.Warn("agent: the Tether gateway's tools are not available", "err", err)
			}
			return
		}
		var next []ServerTool
		for _, info := range list {
			level, ok := tetherTools[info.Name]
			if !ok {
				continue
			}
			name := info.Name
			schema := info.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			next = append(next, ServerTool{
				Spec: ToolSpec{Name: name, Description: info.Description, InputSchema: schema, Level: level},
				Run: func(ctx context.Context, input map[string]any) (string, error) {
					out, err := link.Call(ctx, name, input)
					return string(out), err
				},
			})
		}
		tools, loaded = next, time.Now()
		slog.Info("agent: Tether tools loaded", "count", len(next), "offered", len(list))
	}
	return func(context.Context) []ServerTool {
		if !link.Configured() {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		if !loading && time.Since(loaded) > 10*time.Minute {
			loading = true
			go load()
		}
		return tools
	}
}
