package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
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
//
// The gateway's definitions are long (written as guidance for coding agents: about 58 KB
// for these 18), so the model is not sent them with every request. It is given each tool
// by name with one line, and the tool tether_describe, which returns a tool's full
// description and arguments from the list held here. It asks once per tool, the first
// time it needs it in a conversation. A definition that is short anyway is sent whole.
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
		full := map[string]tetherlink.ToolInfo{}
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
			spec := ToolSpec{Name: name, Description: info.Description, InputSchema: schema, Level: level}
			brief := definitionSize(info) > wholeDefinition
			if brief {
				full[name] = tetherlink.ToolInfo{Name: name, Description: info.Description, InputSchema: schema}
				spec.Description = oneLine(info.Description) + " (Arguments: " + describeTool + " first.)"
				spec.InputSchema = map[string]any{"type": "object", "additionalProperties": true}
			}
			next = append(next, ServerTool{
				Spec: spec,
				Run: func(ctx context.Context, input map[string]any) (string, error) {
					out, err := link.Call(ctx, name, input)
					if err != nil && brief {
						// Most likely the arguments were guessed.
						return string(out), fmt.Errorf("%w\n(For this tool's arguments, call %s with {\"names\":[%q]}.)", err, describeTool, name)
					}
					return string(out), err
				},
			})
		}
		if len(full) > 0 {
			next = append(next, describer(full))
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

// describeTool is the tool that returns a gateway tool's full definition.
const describeTool = "tether_describe"

// wholeDefinition is the size (description and arguments, in bytes) up to which a tool's
// definition is sent with every request: describing it on demand would cost more.
const wholeDefinition = 600

func definitionSize(info tetherlink.ToolInfo) int {
	schema, _ := json.Marshal(info.InputSchema)
	return len(info.Description) + len(schema)
}

var markup = regexp.MustCompile("[*`]")

// oneLine is a description's first line, without markup, short enough to send every time.
func oneLine(description string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(description), "\n")
	line = strings.TrimSpace(markup.ReplaceAllString(line, ""))
	if r := []rune(line); len(r) > 220 {
		line = string(r[:220])
		// Back to the end of a sentence (not the stop in "e.g."), or else of a word.
		cut := -1
		for i := len(line) - 2; i > 80; i-- {
			if line[i] == '.' && line[i+1] == ' ' && !strings.HasSuffix(line[:i], "e.g") && !strings.HasSuffix(line[:i], "i.e") {
				cut = i + 1
				break
			}
		}
		if cut > 0 {
			line = line[:cut]
		} else if i := strings.LastIndex(line, " "); i > 0 {
			line = strings.TrimRight(line[:i], " ,;:(—-") + "…"
		}
	}
	if line != "" && !strings.HasSuffix(line, ".") && !strings.HasSuffix(line, "…") {
		line += "."
	}
	return line
}

// describer is tether_describe: the full definitions of the tools that were given in one
// line, from the list read from the gateway.
func describer(full map[string]tetherlink.ToolInfo) ServerTool {
	names := make([]string, 0, len(full))
	for n := range full {
		names = append(names, n)
	}
	slices.Sort(names)
	return ServerTool{
		Spec: ToolSpec{
			Name:  describeTool,
			Level: LevelRead,
			Description: "The full description and arguments of the Torque and Tesseract tools whose line ends \"(Arguments: " + describeTool + " first.)\". " +
				"Those tools are listed without their arguments. Call this once for a tool before you first use it in a conversation; after that you have it. Several names at once is fine.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The tools' names, for example [\"torque_task_list\"]. Up to 6."},
				},
				"required":             []string{"names"},
				"additionalProperties": false,
			},
		},
		Run: func(_ context.Context, input map[string]any) (string, error) {
			asked, _ := input["names"].([]any)
			if name, ok := input["name"].(string); ok && len(asked) == 0 {
				asked = []any{name}
			}
			if len(asked) == 0 {
				return "", errors.New("give names: the tools to describe")
			}
			if len(asked) > 6 {
				return "", errors.New("up to 6 names at a time")
			}
			type definition struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Arguments   map[string]any `json:"arguments"`
			}
			out := []definition{}
			var unknown []string
			for _, v := range asked {
				name, _ := v.(string)
				info, ok := full[name]
				if !ok {
					unknown = append(unknown, name)
					continue
				}
				out = append(out, definition{Name: info.Name, Description: info.Description, Arguments: info.InputSchema})
			}
			if len(out) == 0 {
				return "", fmt.Errorf("%s: not tools that need describing. The ones that do: %s", strings.Join(unknown, ", "), strings.Join(names, ", "))
			}
			raw, err := json.Marshal(map[string]any{"tools": out, "unknown": unknown})
			return string(raw), err
		},
	}
}
