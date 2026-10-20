package account

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Capture metadata is a bounded client assertion. References are not fetched,
// preset defaults are not evaluated, and none of these fields grants authority.
// Content remains Markdown in fragmentSubmission.Content; HTML is a client cache.
type captureMetadata struct {
	SchemaVersion int                `json:"schemaVersion"`
	Client        captureClient      `json:"client"`
	Title         string             `json:"title,omitempty"`
	Kind          string             `json:"kind,omitempty"`
	Projects      []string           `json:"projects,omitempty"`
	Contexts      []string           `json:"contexts,omitempty"`
	Priority      string             `json:"priority,omitempty"`
	DueDate       string             `json:"dueDate,omitempty"`
	References    []captureReference `json:"references,omitempty"`
	Preset        *capturePreset     `json:"preset,omitempty"`
}

type captureClient struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type captureReference struct {
	Relation string `json:"relation"` // mention | link
	Provider string `json:"provider"`
	Instance string `json:"instance"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
	Label    string `json:"label,omitempty"`
}

type capturePreset struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	AppliedBy string `json:"appliedBy"` // manual | context-suggestion | context-rule
}

func captureText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && metadataOK(s, max) && (!required || strings.TrimSpace(s) != "")
}

func validCaptureMetadata(m *captureMetadata) bool {
	if m == nil {
		return true // Existing Frag requests retain their original wire/hash shape.
	}
	if m.SchemaVersion != 1 || !captureText(m.Client.Name, 64, true) || !captureText(m.Client.Version, 64, true) || !captureText(m.Title, 200, false) || !slices.Contains([]string{"", "note", "todo", "scratch"}, m.Kind) || !slices.Contains([]string{"", "A", "B", "C"}, m.Priority) {
		return false
	}
	if m.DueDate != "" {
		if _, err := time.Parse("2006-01-02", m.DueDate); err != nil {
			return false
		}
	}
	for _, values := range [][]string{m.Projects, m.Contexts} {
		if len(values) > 32 {
			return false
		}
		for _, value := range values {
			if !captureText(value, 100, true) {
				return false
			}
		}
	}
	if len(m.References) > 32 {
		return false
	}
	for _, ref := range m.References {
		if !slices.Contains([]string{"mention", "link"}, ref.Relation) || !captureText(ref.Provider, 64, true) || !captureText(ref.Instance, 200, true) || !captureText(ref.Kind, 64, true) || !captureText(ref.ID, 200, true) || !captureText(ref.Revision, 200, false) || !captureText(ref.Label, 200, false) {
			return false
		}
	}
	if p := m.Preset; p != nil {
		if !captureText(p.ID, 100, true) || !captureText(p.Revision, 100, true) || !slices.Contains([]string{"manual", "context-suggestion", "context-rule"}, p.AppliedBy) {
			return false
		}
	}
	raw, err := json.Marshal(m)
	return err == nil && len(raw) <= 16*1024
}
