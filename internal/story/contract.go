// Package story keeps the owner's video scripts and storyboards (docs/story.md): the
// contract story/v1 (a manifest per script, an item per scene, an item per feedback
// round, all in Caster's "story" namespace), a working copy of them, and the writes.
package story

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stages a script moves through, in board order.
var Stages = []string{"idea", "outline", "draft", "review", "revising", "approved", "production"}

// Sections of a scene, in the team's order (the video-script-craft skill).
var Sections = []string{"Visual", "On-screen text", "Narration", "Notes"}

var (
	slugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	sceneRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// ValidSlug says whether s may be a project or script slug: lowercase letters, digits and
// '-', so the '.' separators in a name stay unambiguous.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// ValidSceneID says whether s may be a scene id (s01, s02, …; any [a-z0-9-]).
func ValidSceneID(s string) bool { return sceneRe.MatchString(s) && !strings.HasPrefix(s, "round-") }

// Kind is what a story item is, from its name.
type Kind int

const (
	KindOther Kind = iota
	KindManifest
	KindScene
	KindRound
)

// Name is a parsed item name in the story namespace: <project>.<script>[.<scene>|.round-<n>].
type Name struct {
	Project, Script, Scene string
	Round                  int
	Kind                   Kind
}

// Key is the script's key, <project>.<script>.
func (n Name) Key() string { return n.Project + "." + n.Script }

// ParseName reads an item name (without the "story/" namespace).
func ParseName(s string) Name {
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 || !ValidSlug(parts[0]) || !ValidSlug(parts[1]) {
		return Name{}
	}
	n := Name{Project: parts[0], Script: parts[1], Kind: KindManifest}
	if len(parts) == 3 {
		if r, ok := strings.CutPrefix(parts[2], "round-"); ok {
			v, err := strconv.Atoi(r)
			if err != nil || v < 1 {
				return Name{}
			}
			n.Round, n.Kind = v, KindRound
		} else if ValidSceneID(parts[2]) {
			n.Scene, n.Kind = parts[2], KindScene
		} else {
			return Name{}
		}
	}
	return n
}

// ManifestName, SceneName and RoundName are the names in the story namespace.
func ManifestName(key string) string         { return key }
func SceneName(key, scene string) string     { return key + "." + scene }
func RoundName(key string, round int) string { return key + ".round-" + strconv.Itoa(round) }
func Ref(name string) string                 { return "caster://story/" + name }
func RefAt(name string, rev int) string      { return Ref(name) + "@" + strconv.Itoa(rev) }

// Doc is an item's text: YAML front matter and a markdown body. Front matter keys the app
// does not know are kept as they are when it writes the item again.
type Doc struct {
	front *yaml.Node // a mapping node
	Body  string
}

// ParseDoc splits front matter from the body. Text without front matter is all body.
func ParseDoc(text string) (*Doc, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	d := &Doc{front: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		d.Body = text
		return d, nil
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, errors.New("front matter is not closed")
	}
	head := rest[:end]
	body := rest[end+4:]
	body = strings.TrimPrefix(body, "\n")
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(head), &root); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	if len(root.Content) == 1 && root.Content[0].Kind == yaml.MappingNode {
		d.front = root.Content[0]
	} else if strings.TrimSpace(head) != "" {
		return nil, errors.New("front matter is not a mapping")
	}
	d.Body = body
	return d, nil
}

// String is the item's text again.
func (d *Doc) String() string {
	var b bytes.Buffer
	b.WriteString("---\n")
	if len(d.front.Content) > 0 {
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		_ = enc.Encode(d.front)
		_ = enc.Close()
	}
	b.WriteString("---\n")
	b.WriteString(d.Body)
	if !strings.HasSuffix(d.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func (d *Doc) find(key string) *yaml.Node {
	for i := 0; i+1 < len(d.front.Content); i += 2 {
		if d.front.Content[i].Value == key {
			return d.front.Content[i+1]
		}
	}
	return nil
}

// Str, Int, Strings and Map read a front matter key ("" or zero when it is absent or of
// another shape).
func (d *Doc) Str(key string) string {
	if n := d.find(key); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

func (d *Doc) Num(key string) float64 {
	if n := d.find(key); n != nil && n.Kind == yaml.ScalarNode {
		v, _ := strconv.ParseFloat(n.Value, 64)
		return v
	}
	return 0
}

func (d *Doc) Strings(key string) []string {
	n := d.find(key)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind == yaml.ScalarNode {
			out = append(out, c.Value)
		}
	}
	return out
}

// Decode reads a key into v (for the structured keys: notes, frames, approved, revs).
func (d *Doc) Decode(key string, v any) error {
	n := d.find(key)
	if n == nil {
		return nil
	}
	return n.Decode(v)
}

// Set writes a key, in place when it is there, at the end when it is not.
func (d *Doc) Set(key string, v any) error {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return err
	}
	if n.Kind == yaml.SequenceNode && n.Style == 0 {
		// Short lists of plain values read best on one line, as the contract shows them.
		simple := len(n.Content) <= 40
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				simple = false
			}
		}
		if simple {
			n.Style = yaml.FlowStyle
		}
	}
	if have := d.find(key); have != nil {
		*have = n
		return nil
	}
	d.front.Content = append(d.front.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}

// ---- Scene bodies ----

// SceneBody is a scene's body split into its sections. Text before the first heading and
// sections with other headings are kept as they are.
type SceneBody struct {
	Lead     string
	Sections []Section
}

type Section struct {
	Heading string
	Text    string
}

var headingRe = regexp.MustCompile(`(?m)^## +(.+?)\s*$`)

// ParseSceneBody splits a body on its "## " headings.
func ParseSceneBody(body string) SceneBody {
	var sb SceneBody
	idx := headingRe.FindAllStringSubmatchIndex(body, -1)
	if len(idx) == 0 {
		sb.Lead = body
		return sb
	}
	sb.Lead = body[:idx[0][0]]
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		text := strings.TrimPrefix(body[m[1]:end], "\n")
		sb.Sections = append(sb.Sections, Section{Heading: body[m[2]:m[3]], Text: strings.TrimRight(text, "\n")})
	}
	return sb
}

// canonical is a heading's known name (any case, "On-screen text" or "Onscreen text").
func canonical(h string) string {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(h, "-", ""), " ", ""))
	for _, s := range Sections {
		if strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), " ", "")) == k {
			return s
		}
	}
	return ""
}

// Get is a known section's text.
func (sb SceneBody) Get(name string) string {
	for _, s := range sb.Sections {
		if canonical(s.Heading) == name {
			return s.Text
		}
	}
	return ""
}

// Put sets a known section's text, in place when it is there; a new one goes in the team's
// order among the known sections.
func (sb *SceneBody) Put(name, text string) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range sb.Sections {
		if canonical(sb.Sections[i].Heading) == name {
			sb.Sections[i].Text = text
			return
		}
	}
	at := len(sb.Sections)
	rank := map[string]int{}
	for i, s := range Sections {
		rank[s] = i
	}
	for i, s := range sb.Sections {
		if c := canonical(s.Heading); c != "" && rank[c] > rank[name] {
			at = i
			break
		}
	}
	sb.Sections = append(sb.Sections[:at], append([]Section{{Heading: name, Text: text}}, sb.Sections[at:]...)...)
}

// String is the body again.
func (sb SceneBody) String() string {
	var b strings.Builder
	b.WriteString(sb.Lead)
	for _, s := range sb.Sections {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
			if strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString("## " + s.Heading + "\n")
		if s.Text != "" {
			b.WriteString(s.Text + "\n")
		}
	}
	return b.String()
}
