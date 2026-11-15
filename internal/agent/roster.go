package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The roster. The assistant writes to a small number of seats, and they fan out to their
// own teams from there. The roster is that list: who each seat is, where to reach it, and
// what it is managing, responsible for or leading now, so the assistant knows who to
// message. The owner keeps it. Most of the time there is one standard roster; a seat may
// instead belong to a named one (a workspace: personal, hollis-labs), which is told beside
// the standard one.

// StandardRoster is the roster a seat is on when it names no other.
const StandardRoster = "standard"

// Seat is one entry on the roster.
type Seat struct {
	// Key is the seat's short name, as the assistant gives it: lowercase, dashes.
	Key string `json:"key"`
	// Name is how the seat is called ("Task Ops Manager").
	Name string `json:"name"`
	// Address is where its mail goes (msg://…).
	Address string `json:"address"`
	// Runs says how the seat runs: "local" (a session on the owner's machine) or "tether"
	// (a seat on the agent system).
	Runs string `json:"runs,omitempty"`
	// Leads is what the seat is managing, responsible for or leading now.
	Leads string `json:"leads"`
	// Roster is the roster it is on; the standard one when empty.
	Roster string `json:"roster,omitempty"`
}

var seatKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// keyFor makes a seat's key from its name.
func keyFor(name string) string {
	key := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(key) > 40 {
		key = strings.Trim(key[:40], "-")
	}
	return key
}

// Roster is the owner's seats, in their order.
func (s *Store) Roster(ctx context.Context) []Seat {
	rows, err := s.db.QueryContext(ctx, `SELECT key, def FROM roster ORDER BY position, key`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Seat
	for rows.Next() {
		var key, def string
		if rows.Scan(&key, &def) != nil {
			continue
		}
		var seat Seat
		_ = json.Unmarshal([]byte(def), &seat)
		seat.Key = key
		out = append(out, seat)
	}
	return out
}

// SaveSeat adds a seat or replaces it.
func (s *Store) SaveSeat(ctx context.Context, seat Seat) error {
	seat.Name = strings.TrimSpace(seat.Name)
	seat.Key = strings.ToLower(strings.TrimSpace(seat.Key))
	if seat.Key == "" {
		seat.Key = keyFor(seat.Name)
	}
	if seat.Name == "" {
		seat.Name = seat.Key
	}
	if !seatKey.MatchString(seat.Key) {
		return errors.New("a seat's key is lowercase letters, digits and dashes, up to 40")
	}
	seat.Address = strings.TrimSpace(seat.Address)
	if !strings.HasPrefix(seat.Address, "msg://") || strings.ContainsAny(seat.Address, " \n\t") {
		return errors.New("a seat needs its address (msg://…)")
	}
	seat.Leads = strings.TrimSpace(seat.Leads)
	if seat.Leads == "" {
		return errors.New("say what the seat is managing or leading: it is how the assistant knows when to write to it")
	}
	if len(seat.Leads) > 1200 || len(seat.Name) > 80 {
		return errors.New("that is too long: keep what the seat leads under 1,200 characters")
	}
	switch seat.Runs = strings.ToLower(strings.TrimSpace(seat.Runs)); seat.Runs {
	case "", "local", "tether":
	default:
		return errors.New("a seat runs local or tether")
	}
	seat.Roster = strings.ToLower(strings.TrimSpace(seat.Roster))
	if seat.Roster == StandardRoster {
		seat.Roster = ""
	}
	if seat.Roster != "" && !seatKey.MatchString(seat.Roster) {
		return errors.New("a roster's name is lowercase letters, digits and dashes")
	}
	def, _ := json.Marshal(seat)
	_, err := s.db.ExecContext(ctx, `INSERT INTO roster(key, def, position) VALUES(?,?,(SELECT COALESCE(MAX(position),0)+1 FROM roster))
		ON CONFLICT(key) DO UPDATE SET def=excluded.def`, seat.Key, string(def))
	return err
}

// DropSeat takes a seat off the roster.
func (s *Store) DropSeat(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM roster WHERE key=?`, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("there is no such seat on the roster")
	}
	return nil
}

// FindSeat is the seat on a roster that a name means: its key, its name or its handle
// (with or without @, compared without case or punctuation), or its address.
func FindSeat(seats []Seat, name string) (Seat, bool) {
	name = strings.TrimSpace(name)
	want := keyFor(strings.TrimPrefix(name, "@"))
	for _, seat := range seats {
		if seat.Address == name || (want != "" && (seat.Key == want || keyFor(seat.Name) == want)) {
			return seat, true
		}
	}
	return Seat{}, false
}

// rosterLines is the roster as the <context> block tells it, and a stamp that changes
// when the roster does.
func rosterLines(seats []Seat) (text, stamp string) {
	if len(seats) == 0 {
		return "", ""
	}
	var order []string
	by := map[string][]Seat{}
	for _, seat := range seats {
		name := seat.Roster
		if name == "" {
			name = StandardRoster
		}
		if _, seen := by[name]; !seen {
			order = append(order, name)
		}
		by[name] = append(by[name], seat)
	}
	var b strings.Builder
	b.WriteString("<roster>\nThe seats you write to. Each leads its own team and passes work on from there, so send to the seat whose part it is and not to anyone beneath it. Give a seat by its key in consult, owner_send and owner_status_ask.\n")
	for _, name := range order {
		if name != StandardRoster || len(order) > 1 {
			fmt.Fprintf(&b, "Roster %s:\n", name)
		}
		for _, seat := range by[name] {
			runs := ""
			if seat.Runs != "" {
				runs = ", " + seat.Runs
			}
			fmt.Fprintf(&b, "- %s (%s%s): %s\n", seat.Key, seat.Name, runs, seat.Leads)
		}
	}
	b.WriteString("</roster>")
	raw, _ := json.Marshal(seats)
	return b.String(), fmt.Sprint(len(raw), "-", hashText(string(raw)))
}

func hashText(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return h
}

// ImportRoster reads seats from a JSON file ({"seats": [...]}) when the owner has none
// yet: a way to start from a list.
func (rt *Runtime) ImportRoster(ctx context.Context, path string) (int, error) {
	if len(rt.store.Roster(ctx)) > 0 {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil // no file: nothing to start from
	}
	var doc struct {
		Seats []Seat `json:"seats"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	n := 0
	for _, seat := range doc.Seats {
		if rt.store.SaveSeat(ctx, seat) == nil {
			n++
		}
	}
	return n, nil
}

// RosterTools are the assistant's tools for the roster.
func (rt *Runtime) RosterTools() ToolSource {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	tools := []ServerTool{
		{
			Spec: ToolSpec{Name: "roster_list", Level: LevelRead,
				Description: "The owner's roster with each seat's address: the few seats you write to, what each is managing or leading now, and which roster it is on. You are told the roster in a <roster> block; use this when you need the addresses or have not been told lately.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
			Run: func(ctx context.Context, _ map[string]any) (string, error) {
				raw, _ := json.Marshal(map[string]any{"seats": rt.store.Roster(ctx)})
				return string(raw), nil
			},
		},
		{
			Spec: ToolSpec{Name: "roster_set", Level: LevelWrite,
				Description: "Add a seat to the owner's roster or change one, when they tell you who to go to for something or that a seat's part has changed (\"Task Ops owns the release train now\"). To change what a seat leads, give its key and the new text; the rest is kept. For a new seat give its name, its address (find it with tether_registry_search) and what it leads. The owner is asked first unless they chose Always.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"key":     str("The seat's key (lowercase, dashes). Made from the name when left out."),
					"name":    str("How the seat is called."),
					"address": str("Its address (msg://…)."),
					"runs":    map[string]any{"type": "string", "enum": []string{"local", "tether"}, "description": "local: a session on the owner's machine. tether: a seat on the agent system."},
					"leads":   str("What it is managing, responsible for or leading now, in a line or two."),
					"roster":  str("The roster it is on. The standard one when left out."),
				}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				get := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
				seat := Seat{Key: get("key"), Name: get("name")}
				if old, ok := FindSeat(rt.store.Roster(ctx), firstOf(seat.Key, seat.Name)); ok {
					seat = old
					if n := get("name"); n != "" {
						seat.Name = n
					}
				}
				if v := get("address"); v != "" {
					seat.Address = v
				}
				if v := get("runs"); v != "" {
					seat.Runs = v
				}
				if v := get("leads"); v != "" {
					seat.Leads = v
				}
				if _, given := in["roster"]; given {
					seat.Roster = get("roster")
				}
				if err := rt.store.SaveSeat(ctx, seat); err != nil {
					return "", err
				}
				return "saved the seat " + firstOf(seat.Key, keyFor(seat.Name)), nil
			},
		},
		{
			Spec: ToolSpec{Name: "roster_remove", Level: LevelConfirm,
				Description: "Take a seat off the owner's roster when they say you should no longer go to it. Nothing about the seat itself changes. Always asks the owner.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": str("The seat's key.")}, "required": []string{"key"}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				key, _ := in["key"].(string)
				if seat, ok := FindSeat(rt.store.Roster(ctx), key); ok {
					key = seat.Key
				}
				if err := rt.store.DropSeat(ctx, key); err != nil {
					return "", err
				}
				return "removed " + key + " from the roster", nil
			},
		},
	}
	return func(context.Context) []ServerTool { return tools }
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
