package work

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Doc is an item's text: YAML front matter and a markdown body, or (Plain) a YAML document
// with no body, as kinds and views are. Keys the app does not know are kept as they are
// when it writes the item again.
type Doc struct {
	front *yaml.Node // a mapping node
	Body  string
	Plain bool
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
	d.Body = strings.TrimPrefix(rest[end+4:], "\n")
	if err := d.setFront(head); err != nil {
		return nil, err
	}
	return d, nil
}

// ParsePlain reads a YAML document (a kind, a view). A leading "---" line is allowed.
func ParsePlain(text string) (*Doc, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimPrefix(text, "---\n")
	d := &Doc{front: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, Plain: true}
	if err := d.setFront(text); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Doc) setFront(head string) error {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(head), &root); err != nil {
		return fmt.Errorf("front matter: %w", err)
	}
	if len(root.Content) == 1 && root.Content[0].Kind == yaml.MappingNode {
		d.front = root.Content[0]
	} else if strings.TrimSpace(head) != "" && len(root.Content) > 0 {
		return errors.New("front matter is not a mapping")
	}
	return nil
}

// String is the item's text again.
func (d *Doc) String() string {
	var b bytes.Buffer
	if !d.Plain {
		b.WriteString("---\n")
	}
	if len(d.front.Content) > 0 {
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		_ = enc.Encode(d.front)
		_ = enc.Close()
	}
	if d.Plain {
		return b.String()
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

// Has says whether the key is there.
func (d *Doc) Has(key string) bool { return d.find(key) != nil }

// Str, Num, Bool and Strings read a key ("" or zero when it is absent or of another shape).
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

func (d *Doc) Bool(key string) bool {
	if n := d.find(key); n != nil && n.Kind == yaml.ScalarNode {
		v, _ := strconv.ParseBool(n.Value)
		return v
	}
	return false
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

// Decode reads a key into v (for the structured keys: fields, links, material, published).
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
	if (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && len(n.Content) == 0 {
		n.Style = yaml.FlowStyle // {} and [] rather than a dangling key
	}
	if have := d.find(key); have != nil {
		*have = n
		return nil
	}
	d.front.Content = append(d.front.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}

// Del takes a key out.
func (d *Doc) Del(key string) {
	for i := 0; i+1 < len(d.front.Content); i += 2 {
		if d.front.Content[i].Value == key {
			d.front.Content = append(d.front.Content[:i], d.front.Content[i+2:]...)
			return
		}
	}
}

// SetField sets one key of the "fields" mapping, keeping the others as they are (nil takes
// it out).
func (d *Doc) SetField(key string, v any) error {
	m := d.find("fields")
	if m == nil || m.Kind != yaml.MappingNode {
		if err := d.Set("fields", map[string]any{}); err != nil {
			return err
		}
		m = d.find("fields")
	}
	m.Style = 0
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if v == nil {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				if len(m.Content) == 0 {
					m.Style = yaml.FlowStyle
				}
				return nil
			}
			var n yaml.Node
			if err := n.Encode(v); err != nil {
				return err
			}
			*m.Content[i+1] = n
			return nil
		}
	}
	if v == nil {
		if len(m.Content) == 0 {
			m.Style = yaml.FlowStyle
		}
		return nil
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return err
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}

// ---- Bodies in sections ----

// Body is an item's body split on its "## " headings. Text before the first heading and
// sections with headings the kind does not name are kept as they are.
type Body struct {
	Lead     string
	Sections []Section
}

type Section struct {
	Heading string
	Text    string
}

var headingRe = regexp.MustCompile(`(?m)^## +(.+?)\s*$`)

// ParseBody splits a body on its "## " headings.
func ParseBody(body string) Body {
	var b Body
	idx := headingRe.FindAllStringSubmatchIndex(body, -1)
	if len(idx) == 0 {
		b.Lead = body
		return b
	}
	b.Lead = body[:idx[0][0]]
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		text := strings.TrimPrefix(body[m[1]:end], "\n")
		b.Sections = append(b.Sections, Section{Heading: body[m[2]:m[3]], Text: strings.TrimRight(text, "\n")})
	}
	return b
}

func norm(h string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "", "_", "").Replace(h))
}

// Get is a section's text (heading matched without case, spaces or dashes).
func (b Body) Get(name string) string {
	for _, s := range b.Sections {
		if norm(s.Heading) == norm(name) {
			return s.Text
		}
	}
	return ""
}

// Put sets a section's text, in place when it is there; a new one goes in the order given
// (the kind's sections) among those it names, else at the end.
func (b *Body) Put(name, text string, order []string) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range b.Sections {
		if norm(b.Sections[i].Heading) == norm(name) {
			b.Sections[i].Text = text
			return
		}
	}
	rank := map[string]int{}
	for i, s := range order {
		rank[norm(s)] = i + 1
	}
	at := len(b.Sections)
	if r := rank[norm(name)]; r > 0 {
		for i, s := range b.Sections {
			if q := rank[norm(s.Heading)]; q > r {
				at = i
				break
			}
		}
	}
	b.Sections = append(b.Sections[:at], append([]Section{{Heading: name, Text: text}}, b.Sections[at:]...)...)
}

// String is the body again.
func (b Body) String() string {
	var w strings.Builder
	w.WriteString(b.Lead)
	for _, s := range b.Sections {
		if w.Len() > 0 && !strings.HasSuffix(w.String(), "\n\n") {
			if strings.HasSuffix(w.String(), "\n") {
				w.WriteString("\n")
			} else {
				w.WriteString("\n\n")
			}
		}
		w.WriteString("## " + s.Heading + "\n")
		if s.Text != "" {
			w.WriteString(s.Text + "\n")
		}
	}
	return w.String()
}
