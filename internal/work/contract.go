// Package work is the Work engine (docs/work.md): typed items, links and saved views kept in
// Caster under the contract work/v1, with three apps as presets over it (Content, Planner,
// Writer). A workspace's index is in the namespace "work"; its kinds, items and views are in
// its own namespace, w-<workspace>.
package work

import (
	"crypto/rand"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// IndexNS is the namespace that lists the workspaces.
const IndexNS = "work"

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	idRe   = regexp.MustCompile(`^[a-z0-9]{8}$`)
	kindRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// ValidSlug says whether s may be a workspace slug: lowercase letters, digits and '-'.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// ValidID says whether s is an item or view id: 8 of [a-z0-9].
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidKind says whether s may be a kind's name.
func ValidKind(s string) bool { return kindRe.MatchString(s) }

// NS is a workspace's namespace.
func NS(workspace string) string { return "w-" + workspace }

// Names in a workspace's namespace.
func ItemName(id string) string   { return "i." + id }
func KindName(kind string) string { return "kind." + kind }
func ViewName(id string) string   { return "v." + id }

// Ref is an item's caster:// reference.
func Ref(ns, name string) string { return "caster://" + ns + "/" + name }

// NameKind is what a name in a workspace's namespace holds.
type NameKind int

const (
	NameOther NameKind = iota
	NameItem
	NameKindDef
	NameView
	NameRound
)

// ParseName reads a name in a workspace's namespace: what it holds, and its id (or kind).
func ParseName(name string) (NameKind, string) {
	switch {
	case strings.HasPrefix(name, "i."):
		if id := name[2:]; ValidID(id) {
			return NameItem, id
		}
	case strings.HasPrefix(name, "kind."):
		if k := name[5:]; ValidKind(k) {
			return NameKindDef, k
		}
	case strings.HasPrefix(name, "v."):
		if id := name[2:]; ValidID(id) {
			return NameView, id
		}
	case strings.HasPrefix(name, "r."):
		return NameRound, name[2:]
	}
	return NameOther, ""
}

// NewID is a fresh 8-character id.
func NewID() string {
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i, c := range b {
		b[i] = abc[int(c)%len(abc)]
	}
	return string(b)
}

// ---- Kinds ----

// Field is one typed field of a kind.
type Field struct {
	Key     string   `yaml:"key" json:"key"`
	Label   string   `yaml:"label,omitempty" json:"label,omitempty"`
	Type    string   `yaml:"type" json:"type"` // text | number | date | enum | ref | refs | url | bool
	Options []string `yaml:"options,omitempty" json:"options,omitempty"`
}

// Kind is what an item is: data, not code. Shipped in presets and copied into a workspace,
// where it can be edited.
type Kind struct {
	Kind     string   `yaml:"kind" json:"kind"`
	Label    string   `yaml:"label" json:"label"`
	Plural   string   `yaml:"plural,omitempty" json:"plural,omitempty"`
	Icon     string   `yaml:"icon,omitempty" json:"icon,omitempty"` // a lucide name
	Stages   []string `yaml:"stages" json:"stages"`
	Fields   []Field  `yaml:"fields" json:"fields"`
	Sections []string `yaml:"sections" json:"sections"`
	Children []string `yaml:"children" json:"children"`
	Color    string   `yaml:"color,omitempty" json:"color,omitempty"`
	Rev      int      `yaml:"-" json:"rev"`
	Problem  string   `yaml:"-" json:"problem,omitempty"`
}

// YAML is the kind as its Caster item.
func (k Kind) YAML() string {
	if k.Stages == nil {
		k.Stages = []string{}
	}
	if k.Fields == nil {
		k.Fields = []Field{}
	}
	if k.Sections == nil {
		k.Sections = []string{}
	}
	if k.Children == nil {
		k.Children = []string{}
	}
	b, _ := yaml.Marshal(k)
	return string(b)
}

// HasStage says whether st is one of the kind's stages.
func (k Kind) HasStage(st string) bool {
	for _, s := range k.Stages {
		if s == st {
			return true
		}
	}
	return false
}

// Field is the kind's field named key.
func (k Kind) Field(key string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// ---- Views ----

// View is a saved way of looking at a workspace; Layout holds a canvas or flow arrangement.
type View struct {
	ID      string         `yaml:"-" json:"id"`
	View    string         `yaml:"view" json:"view"` // board | calendar | outline | canvas | flow
	Title   string         `yaml:"title" json:"title"`
	Kinds   []string       `yaml:"kinds" json:"kinds"`
	Filter  map[string]any `yaml:"filter,omitempty" json:"filter,omitempty"`
	GroupBy string         `yaml:"group_by,omitempty" json:"group_by,omitempty"`
	DateFld string         `yaml:"date_field,omitempty" json:"date_field,omitempty"`
	Root    string         `yaml:"root,omitempty" json:"root,omitempty"`
	Rel     []string       `yaml:"rel,omitempty" json:"rel,omitempty"`
	Layout  map[string]any `yaml:"layout,omitempty" json:"layout,omitempty"`
	Rev     int            `yaml:"-" json:"rev"`
	Problem string         `yaml:"-" json:"problem,omitempty"`
}

// Views a workspace may hold.
var ViewKinds = []string{"board", "calendar", "outline", "canvas", "flow"}

// Relations every workspace knows; a kind set may add more.
var Relations = []string{"contains", "depends-on", "leads-to", "appears-in", "uses", "relates"}

// Link is a typed relation from one item to another.
type Link struct {
	Rel string `yaml:"rel" json:"rel"`
	To  string `yaml:"to" json:"to"`
}

// Material is a reference to something kept elsewhere, never a copy.
type Material struct {
	Ref     string `yaml:"ref" json:"ref"`
	Caption string `yaml:"caption,omitempty" json:"caption,omitempty"`
}

// Published is one entry of an item's publication log.
type Published struct {
	At    string `yaml:"at" json:"at"`
	Where string `yaml:"where" json:"where"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Note  string `yaml:"note,omitempty" json:"note,omitempty"`
}
