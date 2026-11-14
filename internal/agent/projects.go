package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Projects. A slot is a kind of work; a project is what the work is on (an app, a book, a
// launch). The owner defines them. A project says where its state lives (a Torque project,
// a Tesseract namespace, a Work workspace, a repository), what selects it (words in the
// window in front: a path, a title, a workspace), and the owner's standing instructions
// for it. The assistant keeps a note per project, as it does per slot. The two are
// independent: engineering on one project, then planning on the same one, then
// engineering on another.

// Project is one of the owner's projects.
type Project struct {
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
	// Match are words that select the project when one is in the title, path, address or
	// details of the window in front (compared without case).
	Match []string `json:"match,omitempty"`
	// Where its state lives.
	Torque    string `json:"torque,omitempty"`    // a Torque project id
	Tesseract string `json:"tesseract,omitempty"` // a Tesseract namespace
	Workspace string `json:"workspace,omitempty"` // a Work workspace (Content, Planner, Writer)
	Repo      string `json:"repo,omitempty"`      // where its code is
	Where     string `json:"where,omitempty"`     // anything else worth knowing where to look
	// Instructions are the owner's standing instructions for work on this project.
	Instructions string `json:"instructions,omitempty"`
	// Note is the assistant's own note for the project, and when it was written.
	Note   string `json:"note,omitempty"`
	NoteAt string `json:"note_written,omitempty"`
}

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Projects are the owner's projects, in their order.
func (s *Store) Projects(ctx context.Context) []Project {
	rows, err := s.db.QueryContext(ctx, `SELECT name, def, note, note_at FROM projects ORDER BY position, name`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Project
	for rows.Next() {
		var name, def, note, at string
		if rows.Scan(&name, &def, &note, &at) != nil {
			continue
		}
		var p Project
		_ = json.Unmarshal([]byte(def), &p)
		p.Name, p.Note, p.NoteAt = name, note, at
		out = append(out, p)
	}
	return out
}

// SaveProject adds a project or replaces its definition (its note is kept).
func (s *Store) SaveProject(ctx context.Context, p Project) error {
	p.Name = strings.ToLower(strings.TrimSpace(p.Name))
	if !projectName.MatchString(p.Name) {
		return errors.New("a project's name is lowercase letters, digits and dashes, up to 40")
	}
	if _, isSlot := slotNamed(p.Name); isSlot {
		return errors.New("that name is one of the kinds of work; pick another")
	}
	var match []string
	for _, m := range p.Match {
		if m = strings.ToLower(strings.TrimSpace(m)); len(m) >= 3 {
			match = append(match, m)
		}
	}
	p.Match = match
	if len(p.Instructions) > 4000 || len(p.About) > 600 || len(p.Where) > 1200 {
		return errors.New("that is too long: keep the instructions under 4,000 characters and the rest short")
	}
	p.Note, p.NoteAt = "", ""
	def, _ := json.Marshal(p)
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(name, def, position) VALUES(?,?,(SELECT COALESCE(MAX(position),0)+1 FROM projects))
		ON CONFLICT(name) DO UPDATE SET def=excluded.def`, p.Name, string(def))
	return err
}

// DropProject removes a project and the assistant's note for it.
func (s *Store) DropProject(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE name=?`, name)
	return err
}

func (s *Store) setProjectNote(ctx context.Context, name, note string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET note=?, note_at=? WHERE name=?`, note, time.Now().UTC().Format("2006-01-02 15:04 UTC"), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("there is no such project")
	}
	return nil
}

// SlotInstructions are the owner's standing instructions for a kind of work, or "".
func (s *Store) SlotInstructions(ctx context.Context, name string) string {
	var text string
	_ = s.db.QueryRowContext(ctx, `SELECT text FROM slot_instructions WHERE name=?`, name).Scan(&text)
	return text
}

// SetSlotInstructions sets them ("" clears).
func (s *Store) SetSlotInstructions(ctx context.Context, name, text string) error {
	if _, ok := slotNamed(name); !ok {
		return errors.New("there is no such context")
	}
	if len(text) > 4000 {
		return errors.New("keep the instructions under 4,000 characters")
	}
	if strings.TrimSpace(text) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM slot_instructions WHERE name=?`, name)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO slot_instructions(name, text) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET text=excluded.text`, name, strings.TrimSpace(text))
	return err
}

// windowText is everything the page said about the window in front, lowercased, for
// matching a project's words against.
func windowText(raw json.RawMessage) string {
	var c struct {
		Focused json.RawMessage `json:"focused"`
	}
	if json.Unmarshal(raw, &c) != nil || len(c.Focused) == 0 {
		return ""
	}
	return strings.ToLower(string(c.Focused))
}

// projectFor is the project the window in front selects, or "".
func projectFor(projects []Project, raw json.RawMessage) string {
	text := windowText(raw)
	if text == "" {
		return ""
	}
	for _, p := range projects {
		for _, m := range p.Match {
			if strings.Contains(text, m) {
				return p.Name
			}
		}
	}
	return ""
}

// projectLines is a project as the <context> block tells it.
func projectLines(p Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s", p.Name)
	if p.About != "" {
		b.WriteString(" (" + p.About + ")")
	}
	b.WriteString(".\n")
	var where []string
	if p.Torque != "" {
		where = append(where, "its tasks are Torque project "+p.Torque)
	}
	if p.Tesseract != "" {
		where = append(where, "what is known about it is in Tesseract under "+p.Tesseract)
	}
	if p.Workspace != "" {
		where = append(where, "its plan is the workspace "+p.Workspace)
	}
	if p.Repo != "" {
		where = append(where, "its code is at "+p.Repo)
	}
	if p.Where != "" {
		where = append(where, p.Where)
	}
	if len(where) > 0 {
		b.WriteString("Where this project's state is: " + strings.Join(where, "; ") + ".\n")
	}
	if p.Instructions != "" {
		b.WriteString("The owner's standing instructions for this project:\n" + p.Instructions + "\n")
	}
	if p.Note != "" {
		fmt.Fprintf(&b, "Your note for this project (you wrote it %s):\n%s\n", p.NoteAt, p.Note)
	} else {
		fmt.Fprintf(&b, "You have no note for this project yet; write one with context_note (project: %q) when something is worth carrying.\n", p.Name)
	}
	return b.String()
}
