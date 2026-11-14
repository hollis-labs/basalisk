package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/hollis-labs/basalisk/internal/fecapture"
)

// A small real PNG (1x1), so the type is decided from the bytes.
var testPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func mediaSource(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			http.Error(w, "the owner's credentials reached a media host", http.StatusTeapot)
			return
		}
		switch r.URL.Path {
		case "/hero.png", "/poster.png":
			w.Write(testPNG)
		case "/moved.png":
			http.Redirect(w, r, "/hero.png", http.StatusFound)
		case "/subs.vtt":
			w.Write([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clipWithMedia(id, src string) map[string]any {
	in := clipIn(id)
	page := in["page"].(map[string]any)
	page["media"] = []map[string]any{
		{"candidateId": "hero", "kind": "image", "role": "hero", "position": 0, "altText": "A hero", "caption": "Figure 1", "sourceContext": "Under heading: Intro",
			"variants": []map[string]any{{"variantId": "original", "kind": "original", "sourceUrl": src + "/moved.png?w=800&token=secret-value", "acquisitionPreference": "browser_preferred", "mimeType": "IMAGE/PNG", "width": 800.4, "height": 600}}},
		{"candidateId": "gone", "kind": "image", "role": "inline", "position": 2,
			"variants": []map[string]any{{"variantId": "original", "kind": "original", "sourceUrl": src + "/missing.png", "acquisitionPreference": "browser_preferred"}}},
		{"candidateId": "video", "kind": "video", "role": "primary", "position": 1, "providerMediaId": "abc123",
			"variants": []map[string]any{
				{"variantId": "stream", "kind": "stream_reference", "sourceUrl": "https://www.youtube.com/watch?v=abc123", "acquisitionPreference": "reference_only"},
				{"variantId": "poster", "kind": "poster", "sourceUrl": src + "/poster.png", "acquisitionPreference": "browser_preferred", "mimeType": "text/html", "width": 1280, "height": 720},
				{"variantId": "subs", "kind": "subtitles", "sourceUrl": src + "/subs.vtt", "acquisitionPreference": "browser_preferred", "width": 10, "expiresAt": "2030-01-01T00:00:00Z"},
				{"variantId": "internal", "kind": "other", "sourceUrl": src + "/x"},
			}},
		{"candidateId": "bad", "kind": "image", "role": "inline", "position": 3,
			"variants": []map[string]any{{"variantId": "original", "kind": "original", "sourceUrl": "javascript:alert(1)"}}},
	}
	page["extensions"] = []map[string]any{
		{"namespace": "provider.youtube", "schemaVersion": "1", "data": map[string]any{"transcript": "Hello there", "chapters": []any{map[string]any{"title": "Start", "url": "https://www.youtube.com/watch?v=abc123&session=s3cret"}}, "accessToken": "never-sent"}},
		{"namespace": "provider.twitter", "schemaVersion": "2", "placement": "extraction", "data": map[string]any{"repliesCaptured": 3}},
		{"namespace": "Provider.Bad", "schemaVersion": "1", "data": map[string]any{"x": 1}},
		{"namespace": "provider.basalisk", "schemaVersion": "1", "data": map[string]any{"forged": true}},
	}
	return in
}

// A clipped page carries its media and metadata as fe-clipper does, and the worker carries
// out FE's instructions: it fetches and uploads what FE asks for (no credentials on the
// media fetch), and completes with one outcome per variant.
func TestFragClipMedia(t *testing.T) {
	src := mediaSource(t)
	old := mediaAddrAllowed
	mediaAddrAllowed = func(netip.Addr) bool { return true } // the test media host is on loopback
	t.Cleanup(func() { mediaAddrAllowed = old })

	f := newDeliveryFixture(t, false)
	personal, _ := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	f.s.db.Exec(`DELETE FROM integrations`)
	f.peer.jwt = "personal-owner-token-0123456789abcdef"
	f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": f.peer.jwt}, 201)
	f.peer.instruct = func(capture string, v map[string]any) map[string]any {
		id := v["client_variant_id"].(string)
		in := map[string]any{"client_variant_id": id, "asset_variant_id": "av-" + id[len(id)-6:]}
		switch {
		case v["transfer_preference"] == "reference_only":
			in["action"] = "reference_only"
		case strings.Contains(id, "subs"):
			in["action"] = "reuse_blob"
			in["digest"] = map[string]string{"algorithm": "sha256", "value": strings.Repeat("a", 64)}
		default:
			in["action"] = "request_upload"
			in["upload_href"] = "/v1/captures/" + capture + "/assets/" + id + "/content"
		}
		return in
	}

	f.c.call("POST", "/api/frag/captures", clipWithMedia("frag-clip-media-00001", src.URL), 202)
	for range 3 {
		if err := personal.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.peer.manifests) != 1 || len(f.peer.completes) != 1 {
		t.Fatal(len(f.peer.manifests), len(f.peer.completes))
	}
	var m struct {
		Document struct {
			Extensions map[string]map[string]any `json:"extensions"`
		}
		Media []struct {
			ID       string `json:"client_media_id"`
			Provider string `json:"provider_media_id"`
			Kind     string
			Role     string
			Position int
			Locator  string `json:"source_locator"`
			Alt      string `json:"alt_text"`
			Caption  string
			Context  string `json:"source_context"`
			Custody  string `json:"default_custody"`
			Variants []map[string]any
		}
		Extraction struct {
			Warnings   []struct{ Code string }
			Extensions map[string]map[string]any `json:"extensions"`
		}
	}
	manifest := f.peer.manifests[0]
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		t.Fatal(err)
	}
	// Order by position, renumbered; the unsafe one left out with a warning.
	if len(m.Media) != 3 || m.Media[0].ID != "hero" || m.Media[1].ID != "video" || m.Media[2].ID != "gone" || m.Media[2].Position != 2 {
		t.Fatalf("media: %+v", m.Media)
	}
	hero := m.Media[0]
	hv := hero.Variants[0]
	if hero.Role != "hero" || hero.Alt != "A hero" || hero.Caption != "Figure 1" || hero.Context != "Under heading: Intro" || hero.Custody != "mirror" ||
		hv["client_variant_id"] != "hero:original:0" || hv["mime_type"] != "image/png" || hv["width"] != 800.0 || hv["transfer_preference"] != "browser_preferred" || hv["requested_custody"] != "mirror" ||
		strings.Contains(hv["source_url"].(string), "secret-value") || hero.Locator != hv["source_url"] {
		t.Fatalf("hero: %+v %+v", hero, hv)
	}
	video := m.Media[1]
	if video.Provider != "abc123" || len(video.Variants) != 3 || video.Custody != "mirror" {
		t.Fatalf("video: %+v", video)
	}
	stream, poster, subs := video.Variants[0], video.Variants[1], video.Variants[2]
	if stream["kind"] != "original" || stream["transfer_preference"] != "reference_only" || stream["requested_custody"] != "reference" ||
		poster["mime_type"] != nil || poster["width"] != 1280.0 ||
		subs["width"] != nil || subs["source_expires_at"] != "2030-01-01T00:00:00.000Z" {
		t.Fatalf("video variants: %+v", video.Variants)
	}
	yt := m.Document.Extensions["provider.youtube"]
	data, _ := json.Marshal(yt)
	if yt == nil || yt["schema_version"] != "1" || !strings.Contains(string(data), "Hello there") || strings.Contains(string(data), "never-sent") || strings.Contains(string(data), "s3cret") {
		t.Fatalf("youtube extension: %s", data)
	}
	if m.Extraction.Extensions["provider.twitter"] == nil || m.Document.Extensions["provider.twitter"] != nil {
		t.Fatal("placement")
	}
	if forged, _ := json.Marshal(m.Document.Extensions["provider.basalisk"]); strings.Contains(string(forged), "forged") {
		t.Fatal("a page overwrote Basalisk's own provenance")
	}
	codes := []string{}
	for _, w := range m.Extraction.Warnings {
		codes = append(codes, w.Code)
	}
	for _, want := range []string{"capture.variant_omitted", "capture.variant_url_omitted", "capture.media_omitted", "capture.extension_omitted", "capture.credentials_omitted"} {
		if !strings.Contains(strings.Join(codes, ","), want) {
			t.Fatalf("warning %s missing: %v", want, codes)
		}
	}

	// The uploads: the hero (through its redirect) and the poster, the bytes as served.
	if string(f.peer.uploads["hero:original:0"]) != string(testPNG) || string(f.peer.uploads["video:poster:1"]) != string(testPNG) || len(f.peer.uploads) != 2 {
		t.Fatalf("uploads: %v", len(f.peer.uploads))
	}
	var done struct {
		Key    string `json:"idempotency_key"`
		Assets []map[string]any
	}
	_ = json.Unmarshal([]byte(f.peer.completes[0]), &done)
	got := map[string]string{}
	for _, a := range done.Assets {
		got[a["client_variant_id"].(string)] = a["outcome"].(string)
	}
	want := map[string]string{"hero:original:0": "uploaded", "video:stream:0": "deferred", "video:poster:1": "uploaded", "video:subs:2": "already_available", "gone:original:0": "failed"}
	if len(got) != len(want) || !strings.HasPrefix(done.Key, "complete-basalisk-") {
		t.Fatalf("outcomes: %v key %s", got, done.Key)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("outcome %s = %s, want %s (all: %v)", k, got[k], v, got)
		}
	}
	var list struct {
		Items []struct{ State string }
	}
	_ = json.Unmarshal(f.c.call("GET", "/api/frag/captures", nil, 200).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].State != "saved" {
		t.Fatalf("%+v", list.Items)
	}
}

// Without the test's permission, media on a private or local address is never fetched: the
// variant fails and the capture still completes.
func TestFragClipMediaPrivateAddressRefused(t *testing.T) {
	src := mediaSource(t)
	f := newDeliveryFixture(t, false)
	personal, _ := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	f.s.db.Exec(`DELETE FROM integrations`)
	f.peer.jwt = "personal-owner-token-0123456789abcdef"
	f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": f.peer.jwt}, 201)
	f.peer.instruct = func(capture string, v map[string]any) map[string]any {
		id := v["client_variant_id"].(string)
		return map[string]any{"client_variant_id": id, "asset_variant_id": "av", "action": "request_upload", "upload_href": "/v1/captures/" + capture + "/assets/" + id + "/content"}
	}
	in := clipIn("frag-clip-media-00002")
	in["page"].(map[string]any)["media"] = []map[string]any{{"candidateId": "hero", "kind": "image", "role": "hero", "position": 0,
		"variants": []map[string]any{{"variantId": "original", "kind": "original", "sourceUrl": src.URL + "/hero.png"}}}}
	f.c.call("POST", "/api/frag/captures", in, 202)
	for range 3 {
		if err := personal.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.peer.uploads) != 0 || len(f.peer.completes) != 1 || !strings.Contains(f.peer.completes[0], "private or local address") {
		t.Fatalf("uploads %d, completes %v", len(f.peer.uploads), f.peer.completes)
	}
}

func TestPublicAddr(t *testing.T) {
	v4 := func(a, b, c, d byte) netip.Addr { return netip.AddrFrom4([4]byte{a, b, c, d}) }
	cases := map[netip.Addr]bool{
		v4(8, 8, 8, 8): true, netip.MustParseAddr("2606:4700::1111"): true,
		v4(127, 0, 0, 1): false, v4(10, 1, 2, 3): false, v4(172, 16, 0, 1): false, v4(192, 168, 1, 1): false,
		v4(100, 101, 102, 103): false, // the tailnet's range
		v4(169, 254, 169, 254): false, v4(0, 0, 0, 0): false, v4(224, 0, 0, 1): false,
		netip.MustParseAddr("::1"): false, netip.MustParseAddr("fd00::1"): false, netip.MustParseAddr("fe80::1"): false,
		netip.AddrFrom16(v4(127, 0, 0, 1).As16()): false, // IPv4-mapped loopback
	}
	for addr, want := range cases {
		if got := publicAddr(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestSniffAndSanitize(t *testing.T) {
	if sniffMedia(testPNG) != "image/png" || sniffMedia([]byte("WEBVTT\n")) != "text/vtt" || sniffMedia([]byte{0, 1, 2, 3, 0xff}) != "application/octet-stream" {
		t.Fatal("sniff")
	}
	for in, want := range map[string]string{
		"https://u:p@example.com/a?x=1&api_key=k#t": "https://example.com/a?x=1#t",
		"https://example.com/a#access_token=abc":    "https://example.com/a",
		"https://example.com/a#/p?Session-Id=1&q=2": "https://example.com/a#/p?q=2",
	} {
		if got := validHTTPURL(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

// FE's instructions are taken only when each names a variant of the manifest once, with a
// known action, and an upload goes to that variant's own path.
func TestReceiptInstructions(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	known := map[string]bool{"v1": true, "v2": true}
	r := fecapture.Receipt{CaptureID: "cap-1"}
	r.AssetInstructions = []json.RawMessage{raw(`{"client_variant_id":"v1","asset_variant_id":"a","action":"request_upload","upload_href":"/v1/captures/cap-1/assets/v1/content"}`), raw(`{"client_variant_id":"v2","asset_variant_id":"b","action":"server_acquire"}`)}
	if _, err := r.Instructions(known); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"client_variant_id":"v1","asset_variant_id":"a","action":"request_upload","upload_href":"/v1/captures/other/assets/v1/content"}`,
		`{"client_variant_id":"v1","asset_variant_id":"a","action":"request_upload","upload_href":"https://elsewhere.example/v1/captures/cap-1/assets/v1/content"}`,
		`{"client_variant_id":"v9","asset_variant_id":"a","action":"server_acquire"}`,
		`{"client_variant_id":"v1","asset_variant_id":"a","action":"delete_everything"}`,
	} {
		r.AssetInstructions = []json.RawMessage{raw(bad)}
		if _, err := r.Instructions(known); err == nil {
			t.Errorf("took %s", bad)
		}
	}
	r.AssetInstructions = []json.RawMessage{raw(`{"client_variant_id":"v2","asset_variant_id":"b","action":"server_acquire"}`), raw(`{"client_variant_id":"v2","asset_variant_id":"b","action":"server_acquire"}`)}
	if _, err := r.Instructions(known); err == nil {
		t.Error("took a variant twice")
	}
}
