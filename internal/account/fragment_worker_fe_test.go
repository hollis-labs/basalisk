package account

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/dataread"
	"github.com/hollis-labs/basalisk/internal/feread"
	"github.com/hollis-labs/libs/security/auth"
)

// Opt-in contract check against an actual FE executable. All processes, keys,
// listeners and databases belong to this test and are removed on completion.
func TestDeliveryRealFE(t *testing.T) {
	binary := os.Getenv("BASALISK_TEST_FE_BINARY")
	if binary == "" {
		t.Skip("set BASALISK_TEST_FE_BINARY to a scoped-capture-capable FE executable")
	}
	for _, denied := range []bool{false, true} {
		name := "capture-replay-complete"
		if denied {
			name = "revoked-service"
		}
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t, true, nilCaptureMetadata())
			// Exercise the independent capture-app path against the real provider.
			f.c.call("DELETE", fmt.Sprintf("/api/fragment-submissions/%d", f.status.Sequence), nil, 200)
			f.device = pairCaptureApp(t, f.c, f.h)
			f.c.call("PUT", "/api/fragment-connections/"+f.device.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
			f.in.CaptureID += "-nil"
			binding := responseAs[captureContextBinding](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/capture-context", map[string]string{"captureID": f.in.CaptureID}, 201))
			f.in.Draft, f.in.SaveContext = nil, nil
			f.in.ContextBindingID, f.in.DraftAt = binding.BindingID, binding.DraftAt
			f.in.SubmittedAt = time.Now().UTC().Format(timestampFormat)
			f.status = responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 202))
			pub, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
			claims := map[string]any{"iss": "tether", "sub": f.config.Subject, "aud": "fragments-engine", "scope": "fragments-engine.capture", "kind": "service", "exp": time.Now().Add(15 * time.Minute).Unix(), "jti": "test-service-token"}
			unsigned := enc(map[string]string{"alg": "EdDSA", "kid": "test-key"}) + "." + enc(claims)
			f.peer.jwt = unsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(unsigned)))
			maintain := strings.Repeat("b", 64)
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+maintain {
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/.well-known/jwks.json":
					json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": "test-key", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
				case "/auth/denylist":
					subs := []string{}
					if denied {
						subs = append(subs, f.config.Subject)
					}
					json.NewEncoder(w).Encode(map[string]any{"jti": []string{}, "sid": []string{}, "sub": subs})
				default:
					w.WriteHeader(404)
				}
			}))
			defer issuer.Close()
			dir := t.TempDir()
			config := filepath.Join(dir, "fragments.yaml")
			owner := filepath.Join(dir, "owner")
			maintainFile := filepath.Join(dir, "maintain")
			for path, value := range map[string]string{config: "database:\n  path: ./fragments.db\nrecall:\n  backend: sqlite\nqueue:\n  auto_drain: false\ningests:\n  - name: disposable-test\n    kind: filesystem_docs\n    enabled: false\n    source:\n      root: ./docs\n    routing:\n      namespace: tests\n    rules: {}\n    labels: {}\n", owner: strings.Repeat("a", 64), maintainFile: maintain} {
				if err = os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// FE CLI doesn't accept an inherited listener; release a random loopback port
			// immediately before starting the bounded disposable child.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			listener.Close()
			childCtx, cancel := context.WithCancel(context.Background())
			cmd := exec.CommandContext(childCtx, binary, "serve-api", "--addr", addr, "--config", config, "--token-file", owner, "--auth-issuer", "tether", "--auth-issuer-url", issuer.URL, "--auth-issuer-token-file", maintainFile)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "HOLLIS_OTEL_DISABLED=1"}
			var logs bytes.Buffer
			cmd.Stdout = &logs
			cmd.Stderr = &logs
			if err = cmd.Start(); err != nil {
				cancel()
				t.Fatal(err)
			}
			defer func() { cancel(); _ = cmd.Wait() }()
			target, _ := url.Parse("http://" + addr)
			ready := false
			probe := &http.Client{Timeout: time.Second}
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
				resp, e := probe.Get(target.String() + "/sysop/")
				if e == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				cancel()
				_ = cmd.Wait()
				t.Fatal("disposable FE failed startup: ", logs.String())
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			lostAccept, lostComplete := false, false
			f.peer.forward = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				record := httptest.NewRecorder()
				proxy.ServeHTTP(record, r)
				if record.Code >= 400 {
					t.Logf("disposable FE response %d: %s", record.Code, record.Body.String())
				}
				if !denied && record.Code >= 200 && record.Code < 300 {
					if r.URL.Path == "/v1/captures" && !lostAccept {
						lostAccept = true
						http.Error(w, "synthetic lost acceptance", 502)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/complete") && !lostComplete {
						lostComplete = true
						http.Error(w, "synthetic lost completion", 502)
						return
					}
				}
				for k, v := range record.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(record.Code)
				io.Copy(w, record.Body)
			})
			f.run(t)
			if denied {
				if st := f.state(t); st.State != "needs_attention" || st.Code != "authorization_required" {
					t.Fatal(st)
				}
				return
			}
			nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments/"+f.in.CaptureID+"/retry", nil, 200)
			f.run(t)
			nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments/"+f.in.CaptureID+"/retry", nil, 200)
			f.run(t)
			st := f.state(t)
			if st.State != "saved" {
				t.Fatalf("actual FE capture failed: %+v", st)
			}
			if !lostAccept || !lostComplete {
				t.Fatal("ambiguous responses not exercised")
			}
			db, err := sql.Open("sqlite", filepath.Join(dir, "fragments.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var metadata, subject string
			if err = db.QueryRow("SELECT metadata_json FROM fragments WHERE id=?", st.FragmentID).Scan(&metadata); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(metadata, "provider.basalisk") || !strings.Contains(metadata, f.session.ID) || !strings.Contains(metadata, "Original") || !strings.Contains(metadata, f.in.CaptureID) {
				t.Fatal("FE did not retain provenance metadata")
			}
			// FE retains the provider extension inside its metadata projection.
			if !strings.Contains(metadata, `"capture_metadata"`) || !strings.Contains(metadata, `"record-1"`) || !strings.Contains(metadata, `"appliedBy":"manual"`) {
				t.Fatal("FE did not retain Nil references/preset metadata")
			}
			var attempts int
			if err = db.QueryRow("SELECT count(*),min(authenticated_subject) FROM capture_attempts").Scan(&attempts, &subject); err != nil {
				t.Fatal(err)
			}
			if attempts != 1 || subject != f.config.Subject {
				t.Fatal("FE replay or verified attribution failed", attempts, subject)
			}
			// A distinct read service uses the same disposable issuer. Neither its
			// grant nor the capture service credential is a Basalisk caller grant.
			readClaims := map[string]any{"iss": "tether", "sub": "svc:basalisk-reader-test", "aud": "fragments-engine", "scope": feread.Scope, "kind": "service", "exp": time.Now().Add(15 * time.Minute).Unix(), "jti": "test-read-token"}
			readUnsigned := enc(map[string]string{"alg": "EdDSA", "kid": "test-key"}) + "." + enc(readClaims)
			readJWT := readUnsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(readUnsigned)))
			readProxy := httptest.NewTLSServer(proxy)
			defer readProxy.Close()
			readProvider, err := feread.New("disposable-fe", readProxy.URL, "svc:basalisk-reader-test", func(context.Context) (string, error) { return readJWT, nil }, readProxy.Client())
			if err != nil {
				t.Fatal(err)
			}
			readHost, err := f.s.NewReadHost(dataread.AuthorizeFunc(readAllow), ReadSource{readProvider, f.in.IntegrationID, f.server.URL})
			if err != nil {
				t.Fatal(err)
			}
			readCtx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: f.session.UserID, Kind: auth.KindOperator})
			page, err := readHost.Query(readCtx, dataread.QueryRequest{Source: readProvider.Describe().Source, Mode: "session", SessionID: f.session.ID})
			if err != nil || len(page.Items) != 1 || page.Status != "complete" || !page.Exhausted {
				t.Fatal("actual FE session read failed", page, err)
			}
			item := page.Items[0]
			if item.Ref.ID != st.FragmentID || item.Version.ID != st.FragmentRevisionID || item.PreviewMarkdown != f.in.Content {
				t.Fatal("FE read lost exact revision/Markdown", item)
			}
			missing := dataread.GetRequest{Ref: item.Ref, Version: dataread.VersionRequest{Mode: "revision", ID: "nonexistent-revision"}}
			if _, err = readHost.Get(readCtx, missing); err != dataread.ErrVersion {
				t.Fatal("actual FE revision fallback", err)
			}
			latest := dataread.GetRequest{Ref: item.Ref, Version: dataread.VersionRequest{Mode: "latest"}}
			if _, err = readHost.Get(readCtx, latest); err != nil {
				t.Fatal("actual FE latest read", err)
			}
			wholePolicy := dataread.AuthorizeFunc(func(c context.Context, p auth.Principal, target dataread.Target) (dataread.Grant, error) {
				g, e := readAllow(c, p, target)
				g.WholeSelection = true
				return g, e
			})
			selectionHost, err := dataread.New(wholePolicy, nil, readProvider)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := selectionHost.Query(readCtx, dataread.QueryRequest{Source: readProvider.Describe().Source, Mode: "selection", Scope: "all"})
			if err != nil || len(selected.Items) != 1 || selected.Items[0].Ref.ID != item.Ref.ID {
				t.Fatal("actual FE selection failed", selected, err)
			}
			// Check the actual FE scope gate directly, independently of the adapter's
			// credential-confusion guard. A capture token must not read or list.
			for _, path := range []string{"/v1/reader/items?scope=all", "/v1/reader/items/" + st.FragmentID} {
				req, _ := http.NewRequest(http.MethodGet, readProxy.URL+path, nil)
				req.Header.Set("Authorization", "Bearer "+f.peer.jwt)
				resp, e := readProxy.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatal("capture token read FE", resp.StatusCode)
				}
			}
			// A read credential cannot mutate FE through the capture route.
			req, _ := http.NewRequest(http.MethodPost, readProxy.URL+"/v1/captures", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+readJWT)
			req.Header.Set("Content-Type", "application/json")
			resp, e := readProxy.Client().Do(req)
			if e != nil {
				t.Fatal(e)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatal("read token wrote FE", resp.StatusCode)
			}
			t.Log("Actual FE verified separate read scope, confirmed-session selection, pinned revision, canonical Markdown, and capture/read separation")
			t.Log("Actual FE confirmed capture, immutable retry, completion retry, and persisted session metadata")
		})
	}
}
