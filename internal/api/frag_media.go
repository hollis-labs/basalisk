package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/basalisk/internal/fecapture"
)

// Media and provider metadata for a clipped page, as fe-clipper carries them.
//
// The extraction (fe-clipper's adapters, run in the page) reports media candidates (images,
// galleries, posters, subtitles, video references) and provider extensions (a YouTube
// transcript, an X or Reddit thread's structure). They are mapped into the fe.capture.v1
// manifest exactly as fe-clipper's envelope.ts maps them (mapMedia, mapVariant,
// providerExtensions), so a page clipped here and one clipped from the extension are the
// same capture to FE.
//
// FE answers each media variant with an instruction. For request_upload the Basalisk server
// fetches the file itself, as the extension does (no cookies, no referrer, 30 seconds, at
// most 5 redirects, 64 MiB), but only from public addresses: the URLs come from a web page,
// and this server, unlike a browser extension, sits on the owner's private network. The
// bytes are held in memory for the upload and never written down. The capture is then
// completed with one outcome per variant.

const (
	maxClipMedia = 64
	// A captioned YouTube video carries a subtitles variant per caption track, with its
	// posters and the stream reference on the same item: 37 on the first one clipped. FE's
	// schema and fe-clipper set no limit; this one only stops something absurd.
	maxClipVariants   = 256
	maxClipExtensions = 16
	// All variants of one clip together (a page of captioned videos stays well under it).
	maxClipTotalVariants = 512
	maxClipExtBytes      = 1024 * 1024
	maxAssetBytes        = 64 * 1024 * 1024
)

type clipVariant struct {
	VariantID             string  `json:"variantId"`
	Kind                  string  `json:"kind"`
	SourceURL             string  `json:"sourceUrl"`
	AcquisitionPreference string  `json:"acquisitionPreference,omitempty"`
	MimeType              string  `json:"mimeType,omitempty"`
	Width                 float64 `json:"width,omitempty"`
	Height                float64 `json:"height,omitempty"`
	DurationSeconds       float64 `json:"durationSeconds,omitempty"`
	ByteSize              float64 `json:"byteSize,omitempty"`
	ExpiresAt             string  `json:"expiresAt,omitempty"`
}

type clipMedia struct {
	CandidateID     string        `json:"candidateId"`
	ProviderMediaID string        `json:"providerMediaId,omitempty"`
	Kind            string        `json:"kind"`
	Role            string        `json:"role"`
	Position        int           `json:"position"`
	AltText         string        `json:"altText,omitempty"`
	Caption         string        `json:"caption,omitempty"`
	SourceContext   string        `json:"sourceContext,omitempty"`
	Variants        []clipVariant `json:"variants"`
}

type clipExtension struct {
	Namespace     string          `json:"namespace"`
	SchemaVersion string          `json:"schemaVersion"`
	Placement     string          `json:"placement,omitempty"`
	Data          json.RawMessage `json:"data"`
}

var (
	mediaKinds    = []string{"image", "video", "audio", "document", "timed_text", "other"}
	mediaRoles    = []string{"primary", "gallery_item", "hero", "inline", "poster", "transcript", "other"}
	variantKinds  = []string{"original", "preview", "thumbnail", "poster", "audio", "subtitles", "transcript", "stream_reference", "other"}
	extNamespace  = regexp.MustCompile(`^provider\.[a-z0-9][a-z0-9_-]*$`)
	mimePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)
	sensitiveKeys = map[string]bool{"accesstoken": true, "apikey": true, "auth": true, "authorization": true, "authtoken": true, "bearer": true, "clientsecret": true, "cookie": true, "credential": true, "csrf": true, "csrftoken": true, "oauthtoken": true, "password": true, "passwd": true, "refreshtoken": true, "session": true, "sessionid": true, "sessiontoken": true, "token": true, "visitordata": true}
	nonAlnum      = regexp.MustCompile(`[^a-z0-9]`)
)

// validClipMedia checks the media and extensions a page reports (sizes and shapes only;
// anything FE cannot take is left out when the manifest is made, with a warning).
func validClipMedia(p *fragPage) string {
	if len(p.Media) > maxClipMedia || len(p.Extensions) > maxClipExtensions {
		return "The clipped page reports too much media."
	}
	total := 0
	for _, m := range p.Media {
		total += len(m.Variants)
	}
	if total > maxClipTotalVariants {
		return "The clipped page reports too much media."
	}
	for _, m := range p.Media {
		if len(m.Variants) == 0 || len(m.Variants) > maxClipVariants || len(m.CandidateID) > 1024 || len(m.ProviderMediaID) > 1024 || len(m.AltText) > 4000 || len(m.Caption) > 4000 || len(m.SourceContext) > 1000 {
			return "The clipped page reports media that is not readable."
		}
		for _, v := range m.Variants {
			if len(v.VariantID) > 255 || len(v.SourceURL) > 8192 || len(v.MimeType) > 255 || len(v.ExpiresAt) > 64 {
				return "The clipped page reports media that is not readable."
			}
		}
	}
	size := 0
	for _, e := range p.Extensions {
		size += len(e.Data)
		if len(e.Namespace) > 128 || len(e.SchemaVersion) > 64 || (len(e.Data) > 0 && !json.Valid(e.Data)) {
			return "The clipped page reports metadata that is not readable."
		}
	}
	if size > maxClipExtBytes {
		return "The clipped page's metadata is too large (at most 1 MiB)."
	}
	return ""
}

func sensitiveName(k string) bool {
	return sensitiveKeys[nonAlnum.ReplaceAllString(strings.ToLower(k), "")]
}

func dropSensitive(q url.Values) bool {
	removed := false
	for k := range q {
		if sensitiveName(k) {
			q.Del(k)
			removed = true
		}
	}
	return removed
}

// sanitizeURL is fe-clipper's sanitizeCapturePageUrl: no user or password, and no query or
// fragment parameter named like a credential.
func sanitizeURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	changed := u.User != nil
	u.User = nil
	q := u.Query()
	if dropSensitive(q) {
		u.RawQuery = q.Encode()
		changed = true
	}
	if f := u.Fragment; f != "" {
		if i := strings.IndexByte(f, '?'); i >= 0 {
			if fq, err := url.ParseQuery(f[i+1:]); err == nil && dropSensitive(fq) {
				u.Fragment = f[:i]
				if e := fq.Encode(); e != "" {
					u.Fragment += "?" + e
				}
				changed = true
			}
		} else if !strings.Contains(f, "=") {
			if sensitiveName(f) {
				u.Fragment = ""
				changed = true
			}
		} else if fq, err := url.ParseQuery(f); err == nil && dropSensitive(fq) {
			u.Fragment = fq.Encode()
			changed = true
		}
	}
	return u.String(), changed
}

// validHTTPURL is fe-clipper's validHttpUrl: the sanitized address when it is http(s).
func validHTTPURL(raw string) string {
	s, _ := sanitizeURL(strings.TrimSpace(raw))
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return s
}

func positiveInt(v float64) (int64, bool) {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int64(math.Round(v)), math.Round(v) >= 1
}

// mapClipMedia is fe-clipper's mapMedia and mapVariant.
func mapClipMedia(list []clipMedia) (media []any, warnings []any) {
	sorted := slices.Clone(list)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	used := map[string]bool{}
	warn := func(code, msg string) { warnings = append(warnings, map[string]string{"code": code, "message": msg}) }
	for idx, m := range sorted {
		if !slices.Contains(mediaKinds, m.Kind) || !slices.Contains(mediaRoles, m.Role) {
			warn("capture.media_omitted", fmt.Sprintf("Media %s had a kind or role FE does not know and was omitted.", clipIdentifier(m.CandidateID, "media")))
			continue
		}
		mediaID := clipSemantic("media", m.CandidateID)
		if used[mediaID] {
			mediaID = clipSemantic("media", fmt.Sprintf("%s:position:%d:index:%d", m.CandidateID, m.Position, idx))
		}
		used[mediaID] = true
		variants := []map[string]any{}
		for vi, v := range m.Variants {
			if v.Kind == "other" || !slices.Contains(variantKinds, v.Kind) {
				warn("capture.variant_omitted", fmt.Sprintf("Internal variant %s on %s was omitted because FE has no matching variant kind.", clipIdentifier(v.VariantID, "variant"), clipIdentifier(m.CandidateID, "media")))
				continue
			}
			src := ""
			if v.SourceURL != "" {
				if src = validHTTPURL(v.SourceURL); src == "" {
					warn("capture.variant_url_omitted", fmt.Sprintf("Variant %s on %s had an unsafe source URL and was omitted.", clipIdentifier(v.VariantID, "variant"), clipIdentifier(m.CandidateID, "media")))
					continue
				}
			}
			ref := v.Kind == "stream_reference" || v.AcquisitionPreference == "reference_only"
			kind := v.Kind
			if kind == "stream_reference" {
				kind = "original"
			}
			out := map[string]any{"client_variant_id": clipSemantic("variant", fmt.Sprintf("%s:%s:%d", mediaID, v.VariantID, vi)), "kind": kind}
			if src != "" {
				out["source_url"] = src
			}
			if t, err := time.Parse(time.RFC3339Nano, v.ExpiresAt); err == nil {
				out["source_expires_at"] = t.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			mime := strings.ToLower(strings.TrimSpace(v.MimeType))
			// FE's schema: a thumbnail or poster is an image; subtitles and transcripts have no size.
			if mimePattern.MatchString(mime) && ((kind != "thumbnail" && kind != "poster") || strings.HasPrefix(mime, "image/")) {
				out["mime_type"] = mime
			}
			if kind != "subtitles" && kind != "transcript" {
				if w, ok := positiveInt(v.Width); ok {
					out["width"] = w
				}
				if h, ok := positiveInt(v.Height); ok {
					out["height"] = h
				}
			}
			if v.DurationSeconds > 0 && !math.IsInf(v.DurationSeconds, 0) {
				out["duration_seconds"] = v.DurationSeconds
			}
			if v.ByteSize >= 0 && v.ByteSize == math.Trunc(v.ByteSize) && v.ByteSize < 1<<53 && v.ByteSize > 0 {
				out["byte_size"] = int64(v.ByteSize)
			}
			if ref {
				out["transfer_preference"], out["requested_custody"] = "reference_only", "reference"
			} else {
				out["transfer_preference"], out["requested_custody"] = "browser_preferred", "mirror"
			}
			variants = append(variants, out)
		}
		if len(variants) == 0 {
			warn("capture.media_omitted", fmt.Sprintf("Media %s at source position %d had no FE-compatible variants; its position remains recorded by this warning.", clipIdentifier(m.CandidateID, "media"), m.Position))
			continue
		}
		item := map[string]any{"client_media_id": mediaID, "kind": m.Kind, "role": m.Role, "position": len(media), "variants": variants}
		if m.ProviderMediaID != "" {
			item["provider_media_id"] = clipSemantic("provider-media", m.ProviderMediaID)
		}
		for _, v := range variants {
			if s, ok := v["source_url"].(string); ok {
				item["source_locator"] = s
				break
			}
		}
		if m.AltText != "" {
			item["alt_text"] = m.AltText
		}
		if m.Caption != "" {
			item["caption"] = m.Caption
		}
		if m.SourceContext != "" {
			item["source_context"] = m.SourceContext
		}
		item["default_custody"] = "reference"
		for _, v := range variants {
			if v["requested_custody"] == "mirror" {
				item["default_custody"] = "mirror"
			}
		}
		media = append(media, item)
	}
	return media, warnings
}

// sanitizeExtValue is fe-clipper's sanitizeExtensionValue: credential-named keys are dropped,
// and an http(s) string is sanitized like a page address.
func sanitizeExtValue(v any) (any, bool) {
	switch t := v.(type) {
	case []any:
		removed := false
		for i, c := range t {
			var r bool
			t[i], r = sanitizeExtValue(c)
			removed = removed || r
		}
		return t, removed
	case map[string]any:
		removed := false
		for k, c := range t {
			if sensitiveName(k) {
				delete(t, k)
				removed = true
				continue
			}
			var r bool
			t[k], r = sanitizeExtValue(c)
			removed = removed || r
		}
		return t, removed
	case string:
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			if s := validHTTPURL(t); s != "" {
				return s, s != t
			}
			return "", true
		}
	}
	return v, false
}

// placeExtensions is fe-clipper's providerExtensions: each extension under the document
// (fragment metadata) or the extraction (the capture attempt), by its placement.
func placeExtensions(list []clipExtension) (document, extraction map[string]any, warnings []any) {
	document, extraction = map[string]any{}, map[string]any{}
	for _, e := range list {
		if !extNamespace.MatchString(e.Namespace) || e.Namespace == "provider.basalisk" {
			warnings = append(warnings, map[string]string{"code": "capture.extension_omitted", "message": "Provider extension " + clipIdentifier(e.Namespace, "?") + " was omitted because its namespace is invalid."})
			continue
		}
		var data any
		if json.Unmarshal(e.Data, &data) != nil {
			continue
		}
		if _, ok := data.(map[string]any); !ok {
			continue
		}
		clean, removed := sanitizeExtValue(data)
		entry := map[string]any{"schema_version": clipIdentifier(e.SchemaVersion, "1"), "data": clean}
		if e.Placement == "extraction" {
			extraction[e.Namespace] = entry
		} else {
			document[e.Namespace] = entry
		}
		if removed {
			warnings = append(warnings, map[string]string{"code": "capture.credentials_omitted", "message": "Credential-shaped data was removed from " + e.Namespace + "."})
		}
	}
	return document, extraction, warnings
}

// ---- Fetching media for an upload ----

func v4Prefix(a, b, c, d byte, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{a, b, c, d}), bits)
}

// Not public though not in Go's private ranges: carrier-grade NAT (and the tailnet), IETF
// protocol assignments, benchmarking, and the NAT64 prefix.
var blockedPrefixes = []netip.Prefix{
	v4Prefix(100, 64, 0, 0, 10),
	v4Prefix(192, 0, 0, 0, 24),
	v4Prefix(198, 18, 0, 0, 15),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// publicAddr says whether media may be fetched from an address: public unicast only.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// mediaAddrAllowed decides by the address connected to; tests replace it to fetch from a
// local server (the port rule below still applies).
var mediaAddrAllowed = func(ap netip.AddrPort) bool { return publicAddr(ap.Addr()) }

// mediaFetchTimeout is the deadline for one whole fetch, body included.
var mediaFetchTimeout = 30 * time.Second

var (
	errNotPublic = errors.New("not a public address")
	errPort      = errors.New("port not allowed")
)

// mediaPorts are the ports media may be fetched from: 80 and 443, and the page's own port
// when its address named another.
func mediaPorts(pageURL string) map[uint16]bool {
	ports := map[uint16]bool{80: true, 443: true}
	if u, err := url.Parse(pageURL); err == nil && u.Port() != "" {
		var p uint16
		if _, err := fmt.Sscan(u.Port(), &p); err == nil && p > 0 {
			ports[p] = true
		}
	}
	return ports
}

func mediaClient(ports map[uint16]bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		// Checked on the address actually connected to, after DNS, on every connection (so on
		// every redirect hop too): a name that resolves to a private address, or changes to
		// one, is refused, and so is a port other than 80, 443 or the page's own.
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return errNotPublic
			}
			if !ports[ap.Port()] {
				return errPort
			}
			if !mediaAddrAllowed(ap) {
				return errNotPublic
			}
			return nil
		},
	}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
	return &http.Client{
		Timeout:   mediaFetchTimeout,
		Transport: transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 || (r.URL.Scheme != "http" && r.URL.Scheme != "https") || r.URL.User != nil {
				return errors.New("redirect refused")
			}
			r.Header.Del("Referer")
			return nil
		},
	}
}

type fetched struct {
	body []byte
	sha  []byte
	mime string
}

// fetchMedia gets one file: no cookies, no referrer, nothing of the owner's, public addresses
// only, at most max bytes. The returned error is a plain reason for FE (never a secret).
func fetchMedia(ctx context.Context, client *http.Client, raw string, max int64) (fetched, string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fetched{}, "the media address is not a web address", false
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return fetched{}, "the media address is not a web address", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15")
	req.Header.Set("Accept", "image/*,video/*,audio/*,text/vtt,*/*;q=0.5")
	res, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errNotPublic) {
			return fetched{}, "the media is on a private or local address", false
		}
		if errors.Is(err, errPort) {
			return fetched{}, "the media is on a port other than 80, 443 or the page's own", false
		}
		return fetched{}, "the media could not be fetched", true
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return fetched{}, fmt.Sprintf("the media answered %d", res.StatusCode), res.StatusCode >= 500 || res.StatusCode == 429 || res.StatusCode == 408
	}
	// The cap is on the bytes read, whatever the response says its length is.
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return fetched{}, "the media could not be read in time", true
	}
	if int64(len(body)) > max {
		return fetched{}, "the media is larger than FE takes", false
	}
	if len(body) == 0 {
		// As fe-clipper: an empty answer is not the file (YouTube's caption URLs answer empty
		// without the player's own token).
		return fetched{}, "the media source returned an empty response", true
	}
	sum := sha256.Sum256(body)
	return fetched{body: body, sha: sum[:], mime: sniffMedia(body)}, "", false
}

// sniffMedia is the type from the bytes, as fe-clipper decides it: an image, video, audio or
// text type, else application/octet-stream.
func sniffMedia(b []byte) string {
	if strings.HasPrefix(strings.TrimPrefix(string(b[:min(len(b), 16)]), "\ufeff"), "WEBVTT") {
		return "text/vtt"
	}
	t, _, _ := strings.Cut(http.DetectContentType(b), ";")
	for _, p := range []string{"image/", "video/", "audio/", "text/", "application/pdf"} {
		if strings.HasPrefix(t, p) {
			return t
		}
	}
	return "application/octet-stream"
}

// A video on YouTube is never fetched as bytes (fe-clipper does not either): its stream is a
// reference; its poster and subtitles are fetched.
func youtubeVideo(kind, rawURL string) bool {
	if kind != "video" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, d := range []string{"youtube.com", "youtu.be", "googlevideo.com", "youtube-nocookie.com"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

type manifestVariant struct {
	mediaKind, kind, source, expires string
}

// manifestVariants reads the frozen manifest's variants by client id, and the page address.
func manifestVariants(manifest string) (map[string]manifestVariant, string) {
	var m struct {
		Source struct {
			Submitted string `json:"submitted_url"`
		} `json:"source"`
		Media []struct {
			Kind     string `json:"kind"`
			Variants []struct {
				ID      string `json:"client_variant_id"`
				Kind    string `json:"kind"`
				Source  string `json:"source_url"`
				Expires string `json:"source_expires_at"`
			} `json:"variants"`
		} `json:"media"`
	}
	out := map[string]manifestVariant{}
	if json.Unmarshal([]byte(manifest), &m) != nil {
		return out, ""
	}
	for _, it := range m.Media {
		for _, v := range it.Variants {
			out[v.ID] = manifestVariant{it.Kind, v.Kind, v.Source, v.Expires}
		}
	}
	return out, m.Source.Submitted
}

func failedOutcome(id, reason string, retryable bool) map[string]any {
	o := map[string]any{"client_variant_id": id, "outcome": "failed", "reason": reason}
	if retryable {
		o["retryable"] = true
	}
	return o
}

// mediaWorkers is how many files are fetched and uploaded at once.
const mediaWorkers = 4

// mediaReserve is the time an attempt keeps back: no new file is started with less left, so
// the outcomes found so far can still be saved.
var mediaReserve = 15 * time.Second

// maxMediaRounds is how many attempts in a row may end with files still to send before the
// rest are given up as failed and the capture completed.
var maxMediaRounds = 20

// errMediaPending means some files are still to be sent: the outcomes so far are saved, and
// the delivery goes on at once with the rest (fragment_worker.go).
var errMediaPending = &fecapture.Failure{Code: "media_in_progress", Retryable: true}

// transferMedia carries out FE's instructions for an accepted manifest and returns one
// outcome per variant for the completion.
//
// A captioned video has dozens of files, more than one delivery attempt (40 s) may fetch, so
// the work resumes: done holds the outcomes of earlier attempts, which are not redone, and
// record is told each new outcome as it is known (the worker saves it with the row). Files go
// four at a time; none is started with less than mediaReserve left in the attempt, and each
// fetch ends with the attempt. When some are left, errMediaPending says to go on. A file
// that cannot be had is an outcome, not an error; FE busy during an upload leaves that file
// for the next attempt.
func transferMedia(ctx context.Context, client *fecapture.Client, origin, bearer, manifest string, receipt fecapture.Receipt, done map[string]map[string]any, record func(map[string]any) error, last bool) ([]any, error) {
	if len(receipt.AssetInstructions) == 0 {
		return []any{}, nil
	}
	variants, page := manifestVariants(manifest)
	known := map[string]bool{}
	for id := range variants {
		known[id] = true
	}
	instructions, err := receipt.Instructions(known)
	if err != nil {
		return nil, err
	}
	// The soonest to expire first, as fe-clipper orders them.
	sort.SliceStable(instructions, func(i, j int) bool {
		a, b := variants[instructions[i].ClientVariantID].expires, variants[instructions[j].ClientVariantID].expires
		return a != "" && (b == "" || a < b)
	})

	var mu sync.Mutex
	got := map[string]map[string]any{}
	for id, o := range done {
		got[id] = o
	}
	settle := func(o map[string]any) {
		mu.Lock()
		got[o["client_variant_id"].(string)] = o
		mu.Unlock()
		if record != nil {
			_ = record(o) // saved best effort: an unsaved outcome is found again next attempt
		}
	}
	work := []fecapture.Instruction{}
	for _, in := range instructions {
		if _, ok := got[in.ClientVariantID]; ok {
			continue
		}
		v := variants[in.ClientVariantID]
		switch in.Action {
		case "reuse_blob":
			o := map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "already_available"}
			if in.Digest != nil {
				o["digest"] = map[string]string{"algorithm": in.Digest.Algorithm, "value": in.Digest.Value}
			}
			got[in.ClientVariantID] = o
		case "server_acquire", "reference_only":
			got[in.ClientVariantID] = map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "deferred"}
		case "rejected":
			reason := strings.TrimSpace(in.Reason)
			if reason == "" {
				reason = "rejected"
			}
			got[in.ClientVariantID] = map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "not_available", "reason": reason}
		case "request_upload":
			switch {
			case v.source == "":
				got[in.ClientVariantID] = map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "not_available", "reason": "the variant has no source address"}
			case youtubeVideo(v.mediaKind, v.source) && v.kind == "original":
				got[in.ClientVariantID] = map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "not_available", "reason": "video streams are kept as references"}
			default:
				work = append(work, in)
			}
		}
	}

	// The last round fetches nothing more: what is left is reported failed below, leaving the
	// attempt its time to complete the capture.
	if len(work) > 0 && !last {
		fetcher := mediaClient(mediaPorts(page))
		// FE's own limit on one file, when it says (GET /v1/capabilities); 64 MiB at most.
		limit := int64(maxAssetBytes)
		if caps, err := client.Capabilities(ctx, origin, bearer); err == nil && caps.Capture.MaxAssetBytes > 0 && caps.Capture.MaxAssetBytes < limit {
			limit = caps.Capture.MaxAssetBytes
		}
		deadline, bounded := ctx.Deadline()
		queue := make(chan fecapture.Instruction)
		var wg sync.WaitGroup
		for range mediaWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for in := range queue {
					if o := sendOne(ctx, client, fetcher, origin, bearer, in, variants[in.ClientVariantID].source, limit); o != nil {
						settle(o)
					}
				}
			}()
		}
		for _, in := range work {
			if ctx.Err() != nil || (bounded && time.Until(deadline) < mediaReserve) {
				break
			}
			queue <- in
		}
		close(queue)
		wg.Wait()
	}

	outcomes := make([]any, 0, len(instructions))
	for _, in := range instructions {
		o, ok := got[in.ClientVariantID]
		if !ok && last {
			// Many attempts have not sent it: say so and complete the capture.
			o, ok = failedOutcome(in.ClientVariantID, "the media could not be sent in time", true), true
		}
		if !ok {
			return nil, errMediaPending
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, nil
}

// sendOne fetches one file and uploads it: its outcome, or nil when it is to be tried again
// in a later attempt (the attempt ended under it, or FE was busy).
func sendOne(ctx context.Context, client *fecapture.Client, fetcher *http.Client, origin, bearer string, in fecapture.Instruction, source string, limit int64) map[string]any {
	f, reason, retry := fetchMedia(ctx, fetcher, source, limit)
	if ctx.Err() != nil {
		return nil // our attempt ended, not the media host's fault
	}
	if reason != "" {
		return failedOutcome(in.ClientVariantID, reason, retry)
	}
	hexSum := hex.EncodeToString(f.sha)
	if in.ExpectedDigest != nil && (in.ExpectedDigest.Algorithm != "sha256" || in.ExpectedDigest.Value != hexSum) {
		return failedOutcome(in.ClientVariantID, "the media does not match the digest FE expected", false)
	}
	if err := client.Upload(ctx, origin, bearer, in.UploadHref, f.mime, f.body, f.sha); err != nil {
		var fail *fecapture.Failure
		if ctx.Err() != nil || (errors.As(err, &fail) && fail.Retryable) {
			return nil
		}
		return failedOutcome(in.ClientVariantID, "FE did not take the upload", false)
	}
	return map[string]any{"client_variant_id": in.ClientVariantID, "outcome": "uploaded", "digest": map[string]string{"algorithm": "sha256", "value": hexSum}, "byte_size": len(f.body)}
}

// mediaSummary is how a capture's media went, for the outbox: counts by outcome and the first
// few reasons a file was not sent (plain words, never an address).
type mediaSummary struct {
	Sent    int      `json:"sent"`
	Linked  int      `json:"linked"`
	Failed  int      `json:"failed"`
	Reasons []string `json:"reasons,omitempty"`
}

// withMediaSummary keeps the summary in the row's receipt (under a key of Basalisk's own,
// which the receipt's readers ignore), so the outbox can say it without a schema change.
func withMediaSummary(receipt string, outcomes []any) string {
	if len(outcomes) == 0 {
		return receipt
	}
	var sum mediaSummary
	for _, o := range outcomes {
		m, _ := o.(map[string]any)
		switch m["outcome"] {
		case "uploaded", "already_available":
			sum.Sent++
		case "deferred":
			sum.Linked++
		default:
			sum.Failed++
			if r, _ := m["reason"].(string); r != "" && len(sum.Reasons) < 3 && !slices.Contains(sum.Reasons, r) {
				sum.Reasons = append(sum.Reasons, r)
			}
		}
	}
	var doc map[string]any
	if json.Unmarshal([]byte(receipt), &doc) != nil {
		return receipt
	}
	doc["basalisk_media"] = sum
	delete(doc, "basalisk_media_progress")
	delete(doc, "basalisk_media_rounds")
	out, err := json.Marshal(doc)
	if err != nil {
		return receipt
	}
	return string(out)
}

func receiptMedia(receipt string) *mediaSummary {
	if !strings.Contains(receipt, "basalisk_media") {
		return nil
	}
	var doc struct {
		Media *mediaSummary `json:"basalisk_media"`
	}
	if json.Unmarshal([]byte(receipt), &doc) != nil {
		return nil
	}
	return doc.Media
}

// mediaProgress is the outcomes earlier attempts saved with the row's receipt, and how many
// attempts ended with files still to send.
func mediaProgress(receipt string) (map[string]map[string]any, int) {
	var doc struct {
		Progress map[string]map[string]any `json:"basalisk_media_progress"`
		Rounds   int                       `json:"basalisk_media_rounds"`
	}
	_ = json.Unmarshal([]byte(receipt), &doc)
	if doc.Progress == nil {
		doc.Progress = map[string]map[string]any{}
	}
	return doc.Progress, doc.Rounds
}

// withMediaProgress keeps the outcomes so far, and the rounds, in the receipt (keys of
// Basalisk's own, which the receipt's readers ignore).
func withMediaProgress(receipt string, progress map[string]map[string]any, rounds int) string {
	var doc map[string]any
	if json.Unmarshal([]byte(receipt), &doc) != nil {
		return receipt
	}
	doc["basalisk_media_progress"] = progress
	doc["basalisk_media_rounds"] = rounds
	out, err := json.Marshal(doc)
	if err != nil {
		return receipt
	}
	return string(out)
}
