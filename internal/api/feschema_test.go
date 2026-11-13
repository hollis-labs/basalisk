package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// FE's own capture contract (testdata/fe-capture-schema, copied from the Fragments Engine at
// the commit in FE_COMMIT; the same files fe-clipper vendors), validated with the library FE
// itself uses. The stub FE checks every manifest and completion against it, so a delivery
// test cannot pass with a document the real FE would refuse.

const feSchemaBase = "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/"

var (
	feSchemasOnce sync.Once
	feSchemas     map[string]*jsonschema.Resolved
	feSchemasErr  error
)

func feSchemaLoader(uri *url.URL) (*jsonschema.Schema, error) {
	if uri.Scheme != "https" || uri.Host != "schemas.hollis-labs.dev" {
		return nil, fmt.Errorf("external schema reference: %s", uri)
	}
	return loadFESchema(path.Base(uri.Path))
}

func loadFESchema(file string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(path.Join("testdata", "fe-capture-schema", file)) //nolint:gosec // a fixed test data directory
	if err != nil {
		return nil, err
	}
	var s jsonschema.Schema
	d := json.NewDecoder(bytes.NewReader(raw))
	if err = d.Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// validateFE checks a document against one of FE's capture schemas ("capture-envelope",
// "capture-completion").
func validateFE(name string, raw []byte) error {
	feSchemasOnce.Do(func() {
		feSchemas = map[string]*jsonschema.Resolved{}
		for _, n := range []string{"capture-envelope", "capture-completion", "reader-item"} {
			s, err := loadFESchema(n + ".schema.json")
			if err == nil {
				var r *jsonschema.Resolved
				r, err = s.Resolve(&jsonschema.ResolveOptions{BaseURI: feSchemaBase + n + ".schema.json", Loader: feSchemaLoader})
				feSchemas[n] = r
			}
			if err != nil {
				feSchemasErr = fmt.Errorf("%s: %w", n, err)
				return
			}
		}
	})
	if feSchemasErr != nil {
		return feSchemasErr
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return err
	}
	return feSchemas[name].Validate(instance)
}

// The contract is loaded and is strict enough to catch the bug the first real capture hit
// (a relative source link).
func TestFESchemaRefusesRelativeSourceLink(t *testing.T) {
	m := map[string]any{
		"schema_version": "fe.capture.v1", "capture_id": "basalisk-x", "captured_at": "2026-11-13T00:00:00Z", "principal_id": "local-user",
		"client":     map[string]string{"kind": "basalisk", "version": "1.0.0"},
		"source":     map[string]any{"submitted_url": "/api/fragment-submissions/1", "canonical_url": "/api/fragment-submissions/1", "provider": "basalisk", "source_registration_id": "basalisk-notes", "source_item_key": "note:x", "segment_key": "root", "canonicalizer": map[string]string{"adapter": "basalisk", "version": "1.0.0"}},
		"document":   map[string]any{"title": "t", "content": map[string]string{"format": "markdown", "body": "b"}},
		"tags":       []string{}, "annotations": []any{}, "media": []any{},
		"extraction": map[string]any{"adapter": "basalisk", "adapter_version": "1.0.0", "observed_capabilities": []string{"title", "body"}, "warnings": []any{}},
	}
	raw, _ := json.Marshal(m)
	if err := validateFE("capture-envelope", raw); err == nil {
		t.Fatal("a relative source link passed FE's schema")
	}
	m["source"].(map[string]any)["submitted_url"] = "http://127.0.0.1:8085/api/fragment-submissions/1"
	m["source"].(map[string]any)["canonical_url"] = "http://127.0.0.1:8085/api/fragment-submissions/1"
	raw, _ = json.Marshal(m)
	if err := validateFE("capture-envelope", raw); err != nil {
		t.Fatalf("an absolute link failed: %v", err)
	}
}
