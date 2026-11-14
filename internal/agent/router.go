package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	librouter "github.com/hollis-labs/libs/core/util/router"
)

// The router stands in front of the model. A message is first put to a set of rules
// (libs core/util/router: conditions on a few plain fields of the message); a rule that
// matches names a tool to call directly, with no model involved. Everything else goes to
// the model as before. A routed call is an ordinary tool call: it is shown, asks permission
// as its level requires, and is kept in the thread, so the model knows later what was done.
//
// The rules that ship are in rules.json. The owner can add their own in a file (see
// RouterConfig.RulesFile): its rules are tried first.

//go:embed rules.json
var defaultRules []byte

// Named is something the desktop has by id and title: an app, a palette command.
type Named struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Hints are what the page knows that the rules need: what can be opened and run by name.
type Hints struct {
	Apps     []Named `json:"apps"`
	Commands []Named `json:"commands"`
}

// Route is what a matching rule decided.
type Route struct {
	Rule string // the rule's id
	// Tool is the tool to call; "" when the rule only says something.
	Tool  string
	Input map[string]any
	// Say is the reply when the call worked ("" leaves the tool's own result as the reply).
	Say string
	// ThenModel hands the result to the model instead of replying with Say.
	ThenModel bool
}

// Router holds the compiled rules.
type Router struct {
	file string // the owner's rules, if any

	mu       sync.Mutex
	compiled *librouter.Router
	rules    []librouter.Rule
	stamp    string // the owner's file as last read: its time and size
	problem  string // why the owner's file is not in use, if it is not
}

// NewRouter compiles the rules that ship, and the owner's file when there is one.
func NewRouter(rulesFile string) *Router {
	r := &Router{file: rulesFile}
	r.load()
	return r
}

// Problem says why the owner's rules file is not in use ("" when it is, or there is none).
func (r *Router) Problem() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.problem
}

// load compiles the rule set again if the owner's file changed. With the lock held or
// from NewRouter.
func (r *Router) load() {
	stamp := "none"
	var own []byte
	if r.file != "" {
		if st, err := os.Stat(r.file); err == nil {
			stamp = st.ModTime().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(st.Size(), 10)
			if stamp == r.stamp {
				return
			}
			own, _ = os.ReadFile(r.file)
		}
	}
	if stamp == r.stamp && r.compiled != nil {
		return
	}
	r.stamp = stamp
	base, err := librouter.ParseRuleSet(defaultRules)
	if err != nil {
		slog.Error("agent: the built-in router rules do not parse", "err", err)
		return
	}
	set, problem := base, ""
	if len(own) > 0 {
		mine, err := librouter.ParseRuleSet(own)
		if err == nil {
			merged := librouter.RuleSet{Version: base.Version + "+" + mine.Version, Strategy: librouter.FirstMatch}
			merged.Rules = append(append(merged.Rules, mine.Rules...), base.Rules...)
			if _, err = librouter.Compile(merged, librouter.Config{}); err == nil {
				set = merged
			}
		}
		if err != nil {
			problem = fmt.Sprintf("%s is not in use: %v", r.file, err)
			slog.Warn("agent: the owner's router rules are not in use", "file", r.file, "err", err)
		}
	}
	compiled, err := librouter.Compile(set, librouter.Config{
		// A decision keeps which rule matched, not what was said.
		Snapshot: func(s librouter.Subject) librouter.Subject { return librouter.Subject{ID: s.ID} },
	})
	if err != nil {
		slog.Error("agent: the router rules do not compile", "err", err)
		return
	}
	r.compiled, r.rules, r.problem = compiled, set.Rules, problem
}

var (
	slashRE  = regexp.MustCompile(`^/([a-z?][a-z0-9-]*)(?:\s+([\s\S]*))?$`)
	spaceRE  = regexp.MustCompile(`\s+`)
	fieldRE  = regexp.MustCompile(`\{([a-z_]+)\}`)
	articles = map[string]bool{"the": true, "a": true, "an": true, "my": true, "up": true, "new": true}
)

// subject is the message as the rules see it. Fields:
//
//	slash, arg       "/open notes" -> slash "open", arg "notes" (arg as typed)
//	said             "yes" when it is not a slash command
//	text             what was said, lower case, one line, without closing punctuation (short messages only)
//	verb, object     its first word, and the rest without a leading article
//	words            how many words
//	app, app_title   the app the object (or arg) names, by id or title
//	command, command_title   the palette command the message (or arg) names, by title or id
//	focused.app      the app of the window in front
//	selection        "yes" when text is selected there
func subject(text string, deskContext json.RawMessage, hints Hints) map[string]string {
	f := map[string]string{}
	text = strings.TrimSpace(text)
	name := ""
	if m := slashRE.FindStringSubmatch(text); m != nil {
		f["slash"] = m[1]
		if arg := strings.TrimSpace(m[2]); arg != "" {
			f["arg"] = arg
			name = normal(arg)
		}
	} else {
		f["said"] = "yes"
		if len(text) <= 160 && !strings.Contains(text, "\n") {
			t := normal(text)
			f["text"] = t
			words := strings.Fields(t)
			f["words"] = strconv.Itoa(len(words))
			if len(words) > 0 {
				f["verb"] = words[0]
				rest := words[1:]
				for len(rest) > 1 && articles[rest[0]] {
					rest = rest[1:]
				}
				if len(rest) > 0 {
					f["object"] = strings.Join(rest, " ")
				}
			}
		}
	}
	// What is named: after a slash command its argument; otherwise the object for an app
	// ("open notes") and the whole message for a command ("tile windows").
	appName, commandName := name, name
	if f["said"] != "" {
		appName, commandName = f["object"], f["text"]
	}
	if n, ok := named(hints.Apps, appName); ok {
		f["app"], f["app_title"] = n.ID, n.Title
	}
	if n, ok := named(hints.Commands, commandName); ok {
		f["command"], f["command_title"] = n.ID, n.Title
	}
	var c struct {
		Focused *struct {
			App       string `json:"app"`
			Selection string `json:"selection"`
		} `json:"focused"`
	}
	if json.Unmarshal(deskContext, &c) == nil && c.Focused != nil {
		if c.Focused.App != "" {
			f["focused.app"] = c.Focused.App
		}
		if strings.TrimSpace(c.Focused.Selection) != "" {
			f["selection"] = "yes"
		}
	}
	return f
}

// normal is text for comparing: lower case, single spaces, no closing punctuation.
func normal(s string) string {
	s = spaceRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
	return strings.TrimRight(s, " .!?…")
}

// named finds the one thing a name means, by id or title. Two things with the same title
// are not guessed between.
func named(list []Named, name string) (Named, bool) {
	if name == "" {
		return Named{}, false
	}
	var found []Named
	for _, n := range list {
		if n.ID == "" {
			continue
		}
		if strings.ToLower(n.ID) == name || normal(n.Title) == name {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		return Named{}, false
	}
	return found[0], true
}

// Route puts a message to the rules. nil means the model answers it.
func (r *Router) Route(ctx context.Context, text string, deskContext json.RawMessage, hints Hints) *Route {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.load()
	compiled, rules := r.compiled, r.rules
	r.mu.Unlock()
	if compiled == nil {
		return nil
	}
	fields := subject(text, deskContext, hints)
	d, err := compiled.Route(ctx, librouter.Subject{ID: "message", Fields: fields})
	if err != nil || d.Outcome != librouter.Routed {
		return nil
	}
	var m *librouter.Match
	for i := range d.Matches {
		if d.Matches[i].Disposition == librouter.Apply && !d.Matches[i].DryRun {
			m = &d.Matches[i]
			break
		}
	}
	if m == nil {
		return nil
	}
	fill := func(s string) (string, bool) {
		ok := true
		out := fieldRE.ReplaceAllStringFunc(s, func(k string) string {
			v, has := fields[k[1:len(k)-1]]
			if !has {
				ok = false
			}
			return v
		})
		return out, ok
	}
	route := &Route{Rule: m.RuleID, Input: map[string]any{}}
	switch {
	case m.Target == "help":
		route.Say = help(rules)
		return route
	case m.Target == "say":
	case strings.HasPrefix(m.Target, "tool:") && toolName.MatchString(strings.TrimPrefix(m.Target, "tool:")):
		route.Tool = strings.TrimPrefix(m.Target, "tool:")
	default:
		// "model", or a target this build does not know: the model answers.
		return nil
	}
	for k, v := range m.Params {
		filled, ok := fill(v)
		switch {
		case k == "_say":
			route.Say = filled
		case k == "_then":
			route.ThenModel = v == "model"
		case strings.HasPrefix(k, "_"):
		case !ok:
			// The rule needs something the message does not have.
			return nil
		default:
			route.Input[k] = filled
		}
	}
	if route.Tool == "" && route.Say == "" {
		return nil
	}
	return route
}

// help lists what the rules offer, from each rule's _help line.
func help(rules []librouter.Rule) string {
	var lines []string
	for _, rule := range rules {
		if h := strings.TrimSpace(rule.Params["_help"]); h != "" && !rule.Disabled {
			lines = append(lines, "- "+h)
		}
	}
	sort.Strings(lines)
	lines = append(lines, "- /new: put this conversation away and start a new one.")
	return "These run directly, without the model:\n\n" + strings.Join(lines, "\n") + "\n\nAnything else is answered by the model."
}

// RuleInfo is one rule, for the interface to list.
type RuleInfo struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	// Help is the rule's line for /help ("" when it has none).
	Help string `json:"help"`
}

// Rules are the rules in use, in the order they are tried, and the owner's file.
func (r *Router) Rules() (rules []RuleInfo, file string) {
	rules = []RuleInfo{}
	if r == nil {
		return rules, ""
	}
	r.mu.Lock()
	r.load()
	defer r.mu.Unlock()
	for _, rule := range r.rules {
		if !rule.Disabled {
			rules = append(rules, RuleInfo{ID: rule.ID, Target: rule.Target, Help: strings.TrimSpace(rule.Params["_help"])})
		}
	}
	return rules, r.file
}
