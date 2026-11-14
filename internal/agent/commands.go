package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/basalisk/internal/assist"
	"github.com/hollis-labs/basalisk/internal/commands"
)

// CommandTools give the assistant the owner's named commands (internal/commands). log, when
// given, records each run with what the assistant did in the owner's name.
func CommandTools(list commands.List, log *assist.Log) ToolSource {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"name":   str("The command's name, from command_list."),
		"host":   str("The ssh host to run it on, from command_list's hosts. Leave out for this machine."),
		"params": map[string]any{"type": "object", "description": "The command's parameters, by name.", "additionalProperties": map[string]any{"type": "string"}},
	}, "required": []string{"name"}, "additionalProperties": false}
	run := func(asked bool) func(ctx context.Context, in map[string]any) (string, error) {
		return func(ctx context.Context, in map[string]any) (string, error) {
			name, _ := in["name"].(string)
			host, _ := in["host"].(string)
			params := map[string]string{}
			if raw, ok := in["params"].(map[string]any); ok {
				for k, v := range raw {
					params[k] = fmt.Sprint(v)
				}
			}
			res, err := list.Run(ctx, name, host, params, asked)
			if err != nil {
				if !asked && strings.Contains(err.Error(), "asks the owner each time") {
					return "", fmt.Errorf("%w: use command_run_asking, which asks them", err)
				}
				return "", err
			}
			if log != nil {
				where := "this machine"
				if host != "" {
					where = host
				}
				_ = log.Add(ctx, assist.Entry{Kind: "command", Summary: fmt.Sprintf("Ran %s on %s (exit %d)", name, where, res.Exit), With: host, Asked: asked, Detail: map[string]any{"subject": res.Line, "body": clip(res.Output, 2000)}})
			}
			raw, _ := json.Marshal(res)
			return string(raw), nil
		}
	}
	tools := []ServerTool{
		{
			Spec: ToolSpec{Name: "command_list", Level: LevelRead,
				Description: "The command lines you may run for the owner, by name: what each does, its parameters, where it may run (this machine, a host, or either), whether it changes anything, and whether it runs outright or asks the owner each time. With them, the hosts in the owner's ssh config. These are the standing approvals for the command line: a command not on the list is not yours to run this way (put it ready in a terminal with terminal_prepare, or hand it to @chief).",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
			Run: func(context.Context, map[string]any) (string, error) {
				all, problem := list.All()
				out := map[string]any{"commands": all, "hosts": list.Hosts(), "owners_file": list.File}
				if problem != nil {
					out["problem"] = problem.Error()
				}
				raw, _ := json.Marshal(out)
				return string(raw), nil
			},
		},
		{
			Spec: ToolSpec{Name: "command_run", Level: LevelAct,
				Description: "Run one of the owner's named commands that is marked to run outright: it runs at once, on this machine or on a host, and you get its output and exit status. A command that changes something runs this way only when the list says so and its checks pass first. One marked ask is refused here: use command_run_asking. Read command_list first for the names, parameters and hosts.",
				InputSchema: schema},
			Run: run(false),
		},
		{
			Spec: ToolSpec{Name: "command_run_asking", Level: LevelConfirm,
				Description: "Run one of the owner's named commands that asks first (a restart, a reload, anything marked ask): the owner is shown it and decides, every time. Say in your message what it will do and what it interrupts before you call it.",
				InputSchema: schema},
			Run: run(true),
		},
	}
	return func(context.Context) []ServerTool { return tools }
}
