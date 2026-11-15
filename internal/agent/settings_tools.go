package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	librouter "github.com/hollis-labs/libs/core/util/router"
)

func rulesDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// SettingsTools exposes only the two existing automation files, never gateway keys,
// grants or arbitrary paths. Writes keep a backup and require the revision just read.
func (rt *Runtime) SettingsTools() ToolSource {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	props := func(extra map[string]any, required ...string) map[string]any {
		p := map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"router", "reflexes"}}}
		for k, v := range extra {
			p[k] = v
		}
		return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
	}
	tools := []ServerTool{
		{Spec: ToolSpec{Name: "agent_settings_read", Level: LevelRead, Description: "Read the agent's model, context instructions, projects, roster, router rules and reflex summaries. No credentials or standing approval grants are returned.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}, Run: func(ctx context.Context, _ map[string]any) (string, error) {
			rules, _ := rt.Router().Rules()
			refs, _ := rt.Reflexes().List(ctx)
			raw, err := json.Marshal(map[string]any{"model": rt.Config().Model, "contexts": rt.SlotList(ctx), "projects": rt.store.Projects(ctx), "roster": rt.store.Roster(ctx), "rules": rules, "reflexes": refs, "edit_instructions": "Use context_instruct, project_define and roster_set. Startup model and connection settings stay in the operator configuration."})
			return string(raw), err
		}},
		{Spec: ToolSpec{Name: "agent_rules_get", Level: LevelRead, Description: "Read the owner's router or reflex configuration JSON and its revision. The built-in definitions are returned separately as examples. Use agent_rules_write to replace only the owner's file.", InputSchema: props(nil, "kind")}, Run: func(ctx context.Context, in map[string]any) (string, error) {
			file, defaults, err := rt.rulesFile(in)
			if err != nil {
				return "", err
			}
			raw, err := readOwnRules(file)
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(map[string]any{"content": string(raw), "revision": rulesDigest(raw), "built_in": json.RawMessage(defaults)})
			return string(out), err
		}},
		{Spec: ToolSpec{Name: "agent_rules_write", Level: LevelConfirm, Description: "Replace the owner's router or reflex rules with validated JSON. Always asks because it changes future automatic behavior. Requires the revision from agent_rules_get; keeps a backup and refuses stale edits. Built-in rules and approval grants are not changed.", InputSchema: props(map[string]any{"content": str("Complete JSON for the owner's rules"), "revision": str("Revision returned by agent_rules_get")}, "kind", "content", "revision")}, Run: func(ctx context.Context, in map[string]any) (string, error) {
			file, _, err := rt.rulesFile(in)
			if err != nil {
				return "", err
			}
			content, ok := in["content"].(string)
			if !ok || len(content) > 256<<10 {
				return "", errors.New("content must be JSON of at most 256 KiB")
			}
			kind, _ := in["kind"].(string)
			if err = validateOwnRules(kind, []byte(content)); err != nil {
				return "", err
			}
			revision, _ := in["revision"].(string)
			// Serialize writes from concurrent runs using the runtime's existing lock.
			rt.mu.Lock()
			defer rt.mu.Unlock()
			before, err := readOwnRules(file)
			if err != nil {
				return "", err
			}
			if revision != rulesDigest(before) {
				return "", errors.New("the rules changed; read them again before editing")
			}
			if err = ctx.Err(); err != nil {
				return "", err
			}
			if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				return "", err
			}
			if len(before) > 0 {
				if err = os.WriteFile(file+".backup-"+time.Now().UTC().Format("20060102T150405.000000000"), before, 0600); err != nil {
					return "", err
				}
			}
			tmp, err := os.CreateTemp(filepath.Dir(file), ".rules-*")
			if err != nil {
				return "", err
			}
			defer func() { _ = os.Remove(tmp.Name()) }()
			if _, err = tmp.WriteString(content); err != nil {
				_ = tmp.Close()
				return "", err
			}
			if err = tmp.Close(); err != nil {
				return "", err
			}
			if err = ctx.Err(); err != nil {
				return "", err
			}
			if err = os.Rename(tmp.Name(), file); err != nil {
				return "", err
			}
			return "Saved. The rules reload on their next evaluation; the previous contents were backed up.", nil
		}},
	}
	return func(context.Context) []ServerTool { return tools }
}

func (rt *Runtime) rulesFile(in map[string]any) (string, []byte, error) {
	kind, _ := in["kind"].(string)
	var file string
	var defaults []byte
	switch kind {
	case "router":
		r := rt.Router()
		if r != nil {
			file = r.file
		}
		defaults = defaultRules
	case "reflexes":
		r := rt.Reflexes()
		if r != nil {
			file = r.file
		}
		defaults = defaultReflexes
	default:
		return "", nil, errors.New("kind must be router or reflexes")
	}
	if file == "" {
		return "", nil, errors.New("no owner rules file is configured")
	}
	return file, defaults, nil
}
func readOwnRules(file string) ([]byte, error) {
	st, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 256<<10 {
		return nil, errors.New("rules must be a regular file of at most 256 KiB")
	}
	return os.ReadFile(file) //nolint:gosec // Fixed operator-configured router/reflex path, never a tool-supplied path.
}
func validateOwnRules(kind string, raw []byte) error {
	if kind == "reflexes" {
		_, err := parseReflexes(raw, map[string]string{})
		return err
	}
	mine, err := librouter.ParseRuleSet(raw)
	if err != nil {
		return err
	}
	base, err := librouter.ParseRuleSet(defaultRules)
	if err != nil {
		return err
	}
	base.Rules = append(mine.Rules, base.Rules...)
	if _, err := librouter.Compile(base, librouter.Config{}); err != nil {
		return fmt.Errorf("router rules: %w", err)
	}
	return nil
}
