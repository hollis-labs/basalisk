package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func clipIn(id string) map[string]any {
	in := fragIn(id, "")
	delete(in, "content")
	in["note"] = "worth a read"
	in["tags"] = []string{"link"}
	in["page"] = map[string]any{
		"title": "A long read", "description": "About things", "language": "en",
		"submittedUrl": "https://example.com/post?utm=1", "canonicalUrl": "https://example.com/post",
		"provider": "web", "segmentKey": "root", "adapter": "generic", "adapterVersion": "1.2.0",
		"markdown": "# A long read\n\nBody text.", "selection": "Body text",
		"capabilities": []string{"title", "body", "made-up"}, "warnings": []map[string]string{{"code": "Bad Code", "message": "fallback used"}},
	}
	return in
}

// A clipped page goes out as fe-clipper's page manifest: the body untouched, the selection
// and the note as annotations, the page's own addresses as the source.
func TestFragClipManifest(t *testing.T) {
	f := newDeliveryFixture(t, false)
	personal, _ := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	f.s.db.Exec(`DELETE FROM integrations`)
	f.peer.jwt = "personal-owner-token-0123456789abcdef"
	f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": f.peer.jwt}, 201)

	q := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", clipIn("frag-clip-0000000001"), 202))
	// The same clip again is the same row.
	again := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", clipIn("frag-clip-0000000001"), 200))
	if again.Sequence != q.Sequence {
		t.Fatal(again)
	}
	for range 3 {
		if err := personal.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if st, _, _, _ := ownerRow(t, f, q.Sequence); st != "saved" || len(f.peer.manifests) != 1 {
		t.Fatal(st, len(f.peer.manifests))
	}
	var m struct {
		Source struct {
			Registration string `json:"source_registration_id"`
			Submitted    string `json:"submitted_url"`
			Canonical    string `json:"canonical_url"`
			Provider     string
			ItemKey      string `json:"source_item_key"`
			Canon        struct{ Adapter, Version string } `json:"canonicalizer"`
		}
		Document struct {
			Title, Description, Language string
			Content                      struct{ Format, Body string }
		}
		Annotations []struct{ Kind, Text string }
		Media       []any
		Extraction  struct {
			Capabilities []string `json:"observed_capabilities"`
			Warnings     []struct{ Code, Message string }
		}
		PageContext struct {
			Selection bool `json:"selection_present"`
		} `json:"page_context"`
		Tags []string
	}
	if err := json.Unmarshal([]byte(f.peer.manifests[0]), &m); err != nil {
		t.Fatal(err)
	}
	s := m.Source
	if s.Registration != "browser-capture" || s.Submitted != "https://example.com/post?utm=1" || s.Canonical != "https://example.com/post" || s.ItemKey != "https://example.com/post" || s.Provider != "web" || s.Canon.Adapter != "generic" {
		t.Fatalf("source: %+v", s)
	}
	// A selection is what is sent: it is the body, and there is no highlight annotation.
	if m.Document.Title != "A long read" || m.Document.Content.Body != "Body text" || m.Document.Language != "en" {
		t.Fatalf("document: %+v", m.Document)
	}
	if len(m.Annotations) != 1 || m.Annotations[0].Kind != "capture_note" || m.Annotations[0].Text != "worth a read" {
		t.Fatalf("annotations: %+v", m.Annotations)
	}
	if strings.Join(m.Extraction.Capabilities, ",") != "title,body" || len(m.Extraction.Warnings) != 1 || m.Extraction.Warnings[0].Code != "capture.adapter_warning" {
		t.Fatalf("extraction: %+v", m.Extraction)
	}
	if !m.PageContext.Selection || len(m.Media) != 0 || strings.Join(m.Tags, ",") != "link" {
		t.Fatal("page context, media or tags")
	}
	if strings.Contains(f.peer.manifests[0], "plan.md") {
		t.Fatal("context reached FE")
	}

	var list struct {
		Items []struct{ Title, URL, Note, Content string }
	}
	_ = json.Unmarshal(f.c.call("GET", "/api/frag/captures", nil, 200).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Title != "A long read" || list.Items[0].URL != "https://example.com/post?utm=1" || list.Items[0].Note != "worth a read" {
		t.Fatalf("list: %+v", list.Items)
	}
}

// Without a selection the whole page is sent, the body untouched.
func TestFragClipWholePage(t *testing.T) {
	f := newDeliveryFixture(t, false)
	personal, _ := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	f.s.db.Exec(`DELETE FROM integrations`)
	f.peer.jwt = "personal-owner-token-0123456789abcdef"
	f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": f.peer.jwt}, 201)
	in := clipIn("frag-clip-0000000009")
	in["page"].(map[string]any)["selection"] = "   "
	f.c.call("POST", "/api/frag/captures", in, 202)
	for range 3 {
		if err := personal.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var m struct {
		Document struct {
			Content struct{ Body string }
		}
		Annotations []struct{ Kind string }
		PageContext struct {
			Selection bool `json:"selection_present"`
		} `json:"page_context"`
	}
	_ = json.Unmarshal([]byte(f.peer.manifests[0]), &m)
	if m.Document.Content.Body != "# A long read\n\nBody text." || len(m.Annotations) != 1 || m.Annotations[0].Kind != "capture_note" || m.PageContext.Selection {
		t.Fatalf("%+v", m)
	}
}

func TestFragClipValidation(t *testing.T) {
	s, _ := newStore(t)
	c := &client{t: t, handler: s.Handler()}
	c.signIn()
	bad := clipIn("frag-clip-0000000002")
	bad["page"].(map[string]any)["submittedUrl"] = "file:///etc/hosts"
	c.call("POST", "/api/frag/captures", bad, 400)
	big := clipIn("frag-clip-0000000003")
	big["page"].(map[string]any)["markdown"] = strings.Repeat("x", maxClipBody+1)
	c.call("POST", "/api/frag/captures", big, 400)
	noteOnly := fragIn("frag-clip-0000000004", "text")
	noteOnly["note"] = "a note without a page"
	c.call("POST", "/api/frag/captures", noteOnly, 400)
}
