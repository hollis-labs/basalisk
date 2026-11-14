// Package scratch is the owner's scratch: quick notes, commands, follow-ups and ideas, each
// one small item of its own. Files and pages of scratch are views put together from items
// (a day, a project, a task), never the unit that is stored. See docs/scratch.md.
package scratch

import (
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/ui/ui-go/directives"
)

// Kinds an item can be. Any other directive is kept as its own kind ("blog-draft").
const (
	KindNote     = "note"
	KindTodo     = "todo"
	KindFollowUp = "follow-up"
	KindIdea     = "idea"
	KindCommand  = "cmd"
	KindQuestion = "question"
)

// Link relations.
const (
	RelProject = "project"
	RelAgent   = "agent"
	RelTask    = "task"
	RelTag     = "tag"
	RelSession = "session"
	// RelBecame records what an item was promoted to: "note:<place>:<path>", "fragment",
	// "agent-task:<id>". A Torque task it became is an ordinary task link.
	RelBecame = "became"
)

// Link ties an item to something: a project, an agent, a task, a tag, a session.
type Link struct {
	Rel   string `json:"rel"`
	Value string `json:"value"`
}

// Parsed is one item as read out of what was typed.
type Parsed struct {
	Kind string
	// Text is the item as it is kept and shown: the directive's own text, or the plain
	// lines, without the marker that said what kind it is.
	Text  string
	Done  bool
	Links []Link
}

// kindAliases are the ways to say a kind. The directives library has its own aliases too
// (n for note, bd for blog-draft).
var kindAliases = map[string]string{
	"note": KindNote, "n": KindNote,
	"todo": KindTodo, "t": KindTodo, "task": KindTodo,
	"follow-up": KindFollowUp, "followup": KindFollowUp, "follow_up": KindFollowUp, "fu": KindFollowUp, "later": KindFollowUp,
	"idea": KindIdea, "i": KindIdea,
	"cmd": KindCommand, "command": KindCommand, "sh": KindCommand,
	"q": KindQuestion, "question": KindQuestion, "ask": KindQuestion,
}

// configRels are the ::config keys that tie the items after it to something.
var configRels = map[string]string{"project": RelProject, "agent": RelAgent, "task": RelTask, "tag": RelTag, "tags": RelTag, "session": RelSession}

var (
	// ":idea ..." at the start of a line is the same as "::idea ...": how it is often typed.
	singleColon = regexp.MustCompile(`(?m)^(\s*):([A-Za-z][A-Za-z0-9_-]*)(\s|$)`)
	checkbox    = regexp.MustCompile(`^\s*(?:[-*]\s*)?\[( |x|X)?\]\s+`)
	taskID      = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,15}-\d{8}-\d{4}\b`)
	// todo.txt's +project and @context, and a #tag. Each must start a word, and an address
	// ("a@b.c") or a heading ("# Title") is not one.
	projectTok = regexp.MustCompile(`(?:^|\s)\+([A-Za-z][\w.-]*)`)
	agentTok   = regexp.MustCompile(`(?:^|\s)@([A-Za-z][\w.-]*)`)
	tagTok     = regexp.MustCompile(`(?:^|\s)#([A-Za-z][\w-]*)`)
	// "… ::idea" at the end of a line: the marker after the thought, as it is often typed.
	trailing = regexp.MustCompile(`\s::?([A-Za-z][A-Za-z0-9_-]*)\s*$`)
)

// Parse reads what was typed into items. Text with no markers is one note. Each directive
// line (::idea ..., ::follow-up ...) starts an item of its own, which takes the plain lines
// under it up to a blank line; other lines are notes. ::config project=x applies to the
// items after it. A leading "$ " is a command, a leading "[ ]" a todo.
func Parse(input string) []Parsed {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	normal := singleColon.ReplaceAllString(input, "$1::$2$3")
	lines := strings.Split(normal, "\n")
	res := directives.Parse(normal, directives.ParserConfig{Source: "scratch"})
	at := map[int]directives.Directive{} // 1-based line -> the directive on it
	for _, d := range res.Directives {
		at[d.Line] = d
	}

	var out []Parsed
	var standing []Link // from ::config, for the items that follow
	cur := Parsed{Kind: KindNote}
	var body []string
	started := false
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		body = nil
		if !started && text == "" {
			return
		}
		cur.Text = text
		if cur.Text == "" {
			started = false
			cur = Parsed{Kind: KindNote}
			return
		}
		finish(&cur, standing)
		out = append(out, cur)
		started = false
		cur = Parsed{Kind: KindNote}
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// ::config k=v, k=v ties the items after it to something. The library folds it into
		// later directives; it is read here so that plain notes after it are tied too.
		if rest, ok := strings.CutPrefix(trimmed, "::config"); ok && (rest == "" || rest[0] == ' ') {
			flush()
			for _, pair := range strings.Split(rest, ",") {
				k, v, found := strings.Cut(strings.TrimSpace(pair), "=")
				rel, known := configRels[strings.ToLower(strings.TrimSpace(k))]
				if !found || !known {
					continue
				}
				for _, one := range strings.Fields(v) {
					standing = addLink(standing, rel, one)
				}
			}
			continue
		}
		d, isDirective := at[i+1]
		if !isDirective || !strings.HasPrefix(trimmed, "::") {
			// A command line or a checkbox is an item of its own, like a directive.
			if strings.HasPrefix(trimmed, "$ ") || checkbox.MatchString(trimmed) {
				flush()
				body = append(body, trimmed)
				started = true
				flush()
				continue
			}
			// A blank line ends an item that a directive started; a plain note may have
			// paragraphs.
			if trimmed == "" && cur.Kind != KindNote {
				flush()
				continue
			}
			if trimmed != "" || len(body) > 0 {
				body = append(body, line)
				started = started || trimmed != ""
			}
			continue
		}
		switch d.Category {
		case directives.CategoryAction:
			flush()
			kind := strings.ToLower(d.Command)
			if k, ok := kindAliases[kind]; ok {
				kind = k
			}
			cur = Parsed{Kind: kind}
			started = true
			if d.Prompt != "" {
				body = append(body, d.Prompt)
			}
		default:
			// Structure and meta directives (context blocks, zoom, retry) are not scratch
			// items: the line is kept as text so nothing typed is lost.
			body = append(body, line)
			started = true
		}
	}
	flush()
	return out
}

// finish reads the markers inside an item's own text and adds the standing links.
func finish(p *Parsed, standing []Link) {
	first, rest, _ := strings.Cut(p.Text, "\n")
	if p.Kind == KindNote {
		// A marker at the end of the first line says what it is, as one at the start does.
		if m := trailing.FindStringSubmatch(first); m != nil && strings.Contains(m[0], "::") {
			kind := strings.ToLower(m[1])
			if k, ok := kindAliases[kind]; ok {
				kind = k
			}
			if kind != "config" {
				p.Kind = kind
				first = strings.TrimSpace(first[:len(first)-len(m[0])])
			}
		}
	}
	if p.Kind == KindNote {
		switch {
		case strings.HasPrefix(first, "$ "):
			p.Kind = KindCommand
			first = strings.TrimSpace(first[2:])
		case checkbox.MatchString(first):
			m := checkbox.FindStringSubmatch(first)
			p.Kind = KindTodo
			p.Done = strings.EqualFold(m[1], "x")
			first = strings.TrimSpace(first[len(m[0]):])
		}
	} else if p.Kind == KindCommand && strings.HasPrefix(first, "$ ") {
		first = strings.TrimSpace(first[2:])
	}
	p.Text = strings.TrimSpace(first + "\n" + rest)
	if rest == "" {
		p.Text = first
	}
	links := append([]Link(nil), standing...)
	// A command's own text is not read for markers: "@" and "#" and "+" mean other things there.
	if p.Kind != KindCommand {
		for _, m := range projectTok.FindAllStringSubmatch(p.Text, -1) {
			links = addLink(links, RelProject, m[1])
		}
		for _, m := range agentTok.FindAllStringSubmatch(p.Text, -1) {
			links = addLink(links, RelAgent, m[1])
		}
		for _, m := range tagTok.FindAllStringSubmatch(p.Text, -1) {
			links = addLink(links, RelTag, m[1])
		}
	}
	for _, id := range taskID.FindAllString(p.Text, -1) {
		links = addLink(links, RelTask, id)
	}
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Rel != links[j].Rel {
			return links[i].Rel < links[j].Rel
		}
		return links[i].Value < links[j].Value
	})
	p.Links = links
}

func addLink(list []Link, rel, value string) []Link {
	value = strings.TrimSpace(value)
	if rel != RelTask {
		value = strings.ToLower(value)
	}
	if value == "" {
		return list
	}
	for _, l := range list {
		if l.Rel == rel && l.Value == value {
			return list
		}
	}
	return append(list, Link{Rel: rel, Value: value})
}
