package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// The owner's standing instructions: how they want things done, whom to write to, the rules
// they set. They are kept here, outside any thread, and sent with every request of the
// running thread, so neither a new conversation (/new) nor a compaction loses them.

// maxStanding bounds the record: it rides on every request.
const maxStanding = 16000

// Standing is the record in force and its revision (0 and "" when there is none).
func (s *Store) Standing(ctx context.Context) (string, int) {
	var text string
	var rev int
	err := s.db.QueryRowContext(ctx, `SELECT text, rev FROM standing ORDER BY rev DESC LIMIT 1`).Scan(&text, &rev)
	if err != nil {
		return "", 0
	}
	return text, rev
}

// SetStanding keeps a new version of the record; the earlier ones stay as history.
func (s *Store) SetStanding(ctx context.Context, text, why, source string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, errors.New("the standing instructions cannot be empty")
	}
	if len(text) > maxStanding {
		return 0, fmt.Errorf("the standing instructions are %d characters; keep them under %d", len(text), maxStanding)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO standing(text, why, source, created_at) VALUES(?,?,?,?)`, text, strings.TrimSpace(why), source, now())
	if err != nil {
		return 0, err
	}
	rev, err := res.LastInsertId()
	return int(rev), err
}

// ImportStanding seeds the record from a file the first time there is none (a later start
// never overwrites it). No file is not an error: there is nothing to start from.
func (rt *Runtime) ImportStanding(ctx context.Context, path string) (bool, error) {
	if _, rev := rt.store.Standing(ctx); rev > 0 {
		return false, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the owner's own starting file, named by the app's config
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return false, nil
	}
	if _, err := rt.store.SetStanding(ctx, string(data), "the starting record", "seed:"+path); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

// standingBlock is the record as the running thread's agent is told it.
func standingBlock(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "\n\n<standing>\n" + strings.TrimSpace(text) + "\n</standing>"
}

// standingTools are the running thread's tools for the record.
func (rt *Runtime) standingTools() []ServerTool {
	return []ServerTool{
		{
			Spec: ToolSpec{Name: "standing_update", Level: LevelAct,
				Description: "Rewrite the owner's standing instructions (the <standing> block): how they want things done, whom you write to, the rules they have set. Use it when the owner sets, changes or drops a standing rule, so it holds in every later conversation, including after /new. " +
					"It replaces the whole record, so start from the <standing> block you were sent and carry forward everything that still holds; change only what the owner changed. Do not use it for where work stands (that is context_note). Earlier versions are kept.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"text": map[string]any{"type": "string", "description": "The whole record, Markdown."},
					"why":  map[string]any{"type": "string", "description": "One line: what the owner changed."},
				}, "required": []string{"text", "why"}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				text, _ := in["text"].(string)
				why, _ := in["why"].(string)
				old, _ := rt.store.Standing(ctx)
				if old != "" && len(strings.TrimSpace(text)) < len(old)/2 {
					return "", fmt.Errorf("the new record is under half the length of the one in force (%d of %d characters): it replaces the whole record, so carry forward what still holds", len(strings.TrimSpace(text)), len(old))
				}
				rev, err := rt.store.SetStanding(ctx, text, why, "agent")
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("kept as version %d; it is sent with every request from now on", rev), nil
			},
		},
	}
}

// keepInputs records what a run is sent: the system prompt (once per distinct text) and
// the names of the tools offered, so a run can be looked into afterwards.
func (s *Store) keepInputs(ctx context.Context, run, system string, tools []string) {
	sum := sha256.Sum256([]byte(system))
	hash := hex.EncodeToString(sum[:])
	_, _ = s.db.ExecContext(ctx, `INSERT INTO prompts(hash, text, created_at) VALUES(?,?,?) ON CONFLICT(hash) DO NOTHING`, hash, system, now())
	if tools == nil {
		tools = []string{}
	}
	names, _ := json.Marshal(tools)
	_, _ = s.db.ExecContext(ctx, `UPDATE runs SET prompt_hash=?, tools=? WHERE id=?`, hash, string(names), run)
}

// RunInputs is what a run was sent: its system prompt and the names of its tools.
func (s *Store) RunInputs(ctx context.Context, run string) (system string, tools []string, err error) {
	var hash, names string
	err = s.db.QueryRowContext(ctx, `SELECT r.prompt_hash, r.tools, COALESCE(p.text, '') FROM runs r LEFT JOIN prompts p ON p.hash = r.prompt_hash WHERE r.id=?`, run).Scan(&hash, &names, &system)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, errors.New("there is no such run")
	}
	if err != nil {
		return "", nil, err
	}
	_ = json.Unmarshal([]byte(names), &tools)
	return system, tools, nil
}
