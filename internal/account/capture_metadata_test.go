package account

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func nilCaptureMetadata() *captureMetadata {
	return &captureMetadata{
		SchemaVersion: 1, Client: captureClient{Name: "nil", Version: "2.0.0"},
		Title: "Review plans", Kind: "todo", Projects: []string{"Basalisk"}, Contexts: []string{"desk"}, Priority: "B", DueDate: "2026-10-20",
		References: []captureReference{{Relation: "mention", Provider: "tesseract", Instance: "personal", Kind: "workspace", ID: "record-1", Revision: "version-1", Label: "team"}},
		Preset:     &capturePreset{ID: "work", Revision: "3", AppliedBy: "manual"},
	}
}

func TestNilCaptureMetadataSurvivesRestartAndDelivery(t *testing.T) {
	m := nilCaptureMetadata()
	f := newDeliveryFixture(t, true, m)
	f.peer.loseAccept = true
	f.run(t)
	if f.state(t).State != "queued" {
		t.Fatal("lost response did not retain queued operation")
	}
	f.s.Close()
	var err error
	f.s, err = Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.h, f.c.handler = f.s.CompanionHandler(), f.s.Handler()
	f.worker, err = f.s.NewFragmentWorker(f.config, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.retry()
	f.run(t)
	if f.state(t).State != "saved" || len(f.peer.accepted) != 1 || len(f.peer.manifests) != 2 || f.peer.manifests[0] != f.peer.manifests[1] {
		t.Fatal("restart/retry failed to preserve one immutable capture")
	}
	var manifest struct {
		Principal string `json:"principal_id"`
		Document  struct {
			Title      string                        `json:"title"`
			Content    struct{ Format, Body string } `json:"content"`
			Extensions map[string]struct {
				Metadata *captureMetadata `json:"capture_metadata"`
			} `json:"extensions"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(f.peer.manifests[0]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Document.Title != m.Title || manifest.Document.Content.Format != "markdown" || manifest.Document.Content.Body != f.in.Content || !reflect.DeepEqual(manifest.Document.Extensions["provider.basalisk"].Metadata, m) {
		t.Fatal("capture content or metadata changed in FE projection")
	}
	if manifest.Principal != f.config.Subject {
		t.Fatal("client metadata changed authenticated principal")
	}
	changed := f.in
	copyMetadata := *m
	copyMetadata.Title = "different"
	changed.Metadata = &copyMetadata
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", changed, 409)
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 200)
}

func TestNilCaptureMetadataRejectsInvalidAssertions(t *testing.T) {
	f := newDeliveryFixture(t, false)
	cases := map[string]func(*captureMetadata){
		"version":        func(m *captureMetadata) { m.SchemaVersion = 2 },
		"client":         func(m *captureMetadata) { m.Client.Name = " " },
		"title":          func(m *captureMetadata) { m.Title = "bad\nheading" },
		"kind":           func(m *captureMetadata) { m.Kind = "execute" },
		"priority":       func(m *captureMetadata) { m.Priority = "admin" },
		"date":           func(m *captureMetadata) { m.DueDate = "2026-02-30" },
		"projects":       func(m *captureMetadata) { m.Projects = make([]string, 33) },
		"context":        func(m *captureMetadata) { m.Contexts = []string{""} },
		"refIdentity":    func(m *captureMetadata) { m.References[0].ID = "" },
		"refRelation":    func(m *captureMetadata) { m.References[0].Relation = "authorize" },
		"presetRevision": func(m *captureMetadata) { m.Preset.Revision = "" },
		"presetMode":     func(m *captureMetadata) { m.Preset.AppliedBy = "inferred-authority" },
		"metadataBudget": func(m *captureMetadata) {
			m.References = nil
			for i := 0; i < 32; i++ {
				m.References = append(m.References, captureReference{Relation: "link", Provider: "fe", Instance: strings.Repeat("i", 200), Kind: "fragment", ID: strings.Repeat("x", 200), Revision: strings.Repeat("r", 200), Label: strings.Repeat("l", 200)})
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.in
			in.CaptureID = "invalid-metadata-" + name
			in.Metadata = nilCaptureMetadata()
			change(in.Metadata)
			nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", in, 400)
		})
	}
	raw, _ := json.Marshal(f.in)
	var request map[string]any
	json.Unmarshal(raw, &request)
	request["captureID"] = "invalid-html-only-metadata"
	request["metadata"] = map[string]any{"schemaVersion": 1, "client": map[string]string{"name": "nil", "version": "2"}, "html": "<p>not canonical</p>"}
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", request, 400)
	var count int
	if err := f.s.db.QueryRow("SELECT count(*) FROM fragment_submissions").Scan(&count); err != nil || count != 1 {
		t.Fatal("invalid capture persisted", count, err)
	}
}
