package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A page clipped in a Browser window: fe-clipper's extraction (Readability, turndown and its
// site adapters, run in the page; frontend/public/clip/extract.js) queued as an owner capture
// and delivered as the same fe.capture.v1 page manifest fe-clipper sends, so FE treats a page
// clipped here and one clipped from a browser extension as the same source. The body is never
// edited: the selection and the note travel as annotations. No media yet.

const (
	maxClipBody    = 512 * 1024
	maxClipRequest = maxClipBody + 96*1024
)

// fragPage is what the extraction observed, as the page reports it.
type fragPage struct {
	Title          string        `json:"title"`
	Description    string        `json:"description,omitempty"`
	Language       string        `json:"language,omitempty"`
	SubmittedURL   string        `json:"submittedUrl"`
	CanonicalURL   string        `json:"canonicalUrl,omitempty"`
	Provider       string        `json:"provider,omitempty"`
	ProviderItemID string        `json:"providerItemId,omitempty"`
	SegmentKey     string        `json:"segmentKey,omitempty"`
	Adapter        string        `json:"adapter,omitempty"`
	AdapterVersion string        `json:"adapterVersion,omitempty"`
	Markdown       string        `json:"markdown"`
	Selection      string        `json:"selection,omitempty"`
	Capabilities   []string      `json:"capabilities,omitempty"`
	Warnings       []clipWarning `json:"warnings,omitempty"`
}

type clipWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var (
	clipProvider = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	clipCode     = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
	clipLanguage = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	// FE's capability vocabulary; the extraction may report more.
	clipCapabilities = map[string]bool{"title": true, "description": true, "body": true, "gallery_manifest": true, "original_media": true, "thumbnail_or_poster": true, "transcript": true}
)

func clipURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(raw) > 4096 {
		return "", false
	}
	return u.String(), true
}

func validClip(p *fragPage, note string) string {
	text := []string{p.Title, p.Description, p.Markdown, p.Selection, note, p.ProviderItemID, p.SegmentKey, p.Adapter, p.AdapterVersion}
	for _, t := range text {
		if !utf8.ValidString(t) || strings.ContainsRune(t, 0) {
			return "The clipped page has invalid text."
		}
	}
	if len(p.Markdown) > maxClipBody || len(p.Selection) > 64*1024 || len(note) > 16*1024 || len(p.Title) > 1000 || len(p.Description) > 4000 || len(p.Warnings) > 32 || len(p.Capabilities) > 16 {
		return "The clipped page is too large (a body of at most 512 KiB)."
	}
	submitted, ok := clipURL(p.SubmittedURL)
	if !ok {
		return "Only web pages (http and https) can be clipped."
	}
	p.SubmittedURL = submitted
	if p.CanonicalURL == "" {
		p.CanonicalURL = submitted
	} else if canonical, ok := clipURL(p.CanonicalURL); ok {
		p.CanonicalURL = canonical
	} else {
		p.CanonicalURL = submitted
	}
	if strings.TrimSpace(p.Markdown) == "" && strings.TrimSpace(p.Selection) == "" && strings.TrimSpace(note) == "" && strings.TrimSpace(p.Title) == "" {
		return "Nothing was extracted from this page."
	}
	return ""
}

// clipIdentifier is fe-clipper's identifier(): trimmed, a fallback when empty, 255 at most.
func clipIdentifier(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	if len(v) > 255 {
		return v[:255]
	}
	return v
}

// clipSemantic is fe-clipper's semanticIdentifier(): the value, or prefix:sha256 when long.
func clipSemantic(prefix, value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 255 {
		return clipIdentifier(value, prefix)
	}
	sum := sha256.Sum256([]byte(value))
	return prefix + ":" + hex.EncodeToString(sum[:])
}

// pageManifest is the fe.capture.v1 envelope for a clipped page, shaped as fe-clipper's
// buildCaptureEnvelope shapes it (client and provenance aside).
func pageManifest(upstream, principal, capturedAt string, p *fragPage, note string, tags []string, provenance map[string]any) map[string]any {
	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	if !clipProvider.MatchString(provider) {
		provider = "web"
	}
	itemID := strings.TrimSpace(p.ProviderItemID)
	key := clipSemantic("web-url", p.CanonicalURL)
	source := map[string]any{
		"source_registration_id": "browser-capture",
		"submitted_url":          p.SubmittedURL,
		"canonical_url":          p.CanonicalURL,
		"provider":               provider,
		"segment_key":            clipIdentifier(p.SegmentKey, "root"),
		"canonicalizer":          map[string]string{"adapter": clipIdentifier(p.Adapter, "generic"), "version": clipIdentifier(p.AdapterVersion, "unknown")},
	}
	if itemID != "" {
		source["provider_item_id"] = clipSemantic("provider-item", itemID)
		key = clipSemantic("provider-item", provider+":"+itemID)
	}
	source["source_item_key"] = key

	doc := map[string]any{"extensions": map[string]any{"provider.basalisk": provenance}}
	if t := strings.TrimSpace(p.Title); t != "" {
		doc["title"] = t
	}
	if d := strings.TrimSpace(p.Description); d != "" {
		doc["description"] = d
	}
	if clipLanguage.MatchString(p.Language) {
		doc["language"] = p.Language
	}
	if p.Markdown != "" {
		doc["content"] = map[string]string{"format": "markdown", "body": p.Markdown}
	}

	annotations := []any{}
	selection := strings.TrimSpace(p.Selection)
	if selection != "" {
		annotations = append(annotations, map[string]any{"annotation_id": "ann-" + token()[:16], "kind": "highlight", "text": selection, "selector": map[string]string{"exact": selection}})
	}
	if n := strings.TrimSpace(note); n != "" {
		annotations = append(annotations, map[string]any{"annotation_id": "ann-" + token()[:16], "kind": "capture_note", "text": n})
	}

	capabilities := []string{}
	seen := map[string]bool{}
	for _, c := range p.Capabilities {
		if clipCapabilities[c] && !seen[c] {
			seen[c] = true
			capabilities = append(capabilities, c)
		}
	}
	warnings := []any{}
	for _, w := range p.Warnings {
		if msg := strings.TrimSpace(w.Message); msg != "" {
			code := w.Code
			if !clipCode.MatchString(code) {
				code = "capture.adapter_warning"
			}
			warnings = append(warnings, map[string]string{"code": code, "message": msg})
		}
	}
	pageContext := map[string]any{"document_url": p.SubmittedURL, "selection_present": selection != ""}
	if p.Title != "" {
		pageContext["document_title"] = p.Title
	}
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"schema_version": "fe.capture.v1", "capture_id": upstream, "captured_at": capturedAt, "principal_id": principal,
		"client":      map[string]string{"kind": "basalisk", "version": "1.0.0"},
		"source":      source,
		"document":    doc,
		"tags":        tags,
		"annotations": annotations,
		"media":       []any{},
		"extraction": map[string]any{
			"adapter": clipIdentifier(p.Adapter, "generic"), "adapter_version": clipIdentifier(p.AdapterVersion, "unknown"),
			"observed_capabilities": capabilities, "warnings": warnings,
		},
		"page_context": pageContext,
	}
}
