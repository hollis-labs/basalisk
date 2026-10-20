package fecapture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/secrets"
)

func TestIssuerResponseAndRotation(t *testing.T) {
	parent := "first-enrollment-parent"
	subject := "svc:test"
	kind, audience, scope := "service", "fragments-engine", Scope
	exp := time.Now().Add(10 * time.Minute)
	issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+parent {
			w.WriteHeader(401)
			return
		}
		claims, _ := json.Marshal(map[string]any{"sub": subject, "kind": kind, "aud": audience, "scope": scope, "exp": exp.Unix()})
		json.NewEncoder(w).Encode(map[string]any{"token": "test." + base64.RawURLEncoding.EncodeToString(claims) + ".sig", "aud": audience, "kind": kind, "scopes": []string{scope}, "expires_at": exp})
	}))
	defer issuer.Close()
	resolver := secrets.New(secrets.WithStateDir(t.TempDir()))
	set := func() {
		t.Helper()
		if err := resolver.Set(context.Background(), "file://delivery/parent", strings.NewReader(parent)); err != nil {
			t.Fatal(err)
		}
	}
	set()
	client, err := New(Config{IssuerURL: issuer.URL, Subject: "svc:test", ParentRef: "file://delivery/parent"}, resolver, issuer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Exchange(context.Background()); err != nil {
		t.Fatal(err)
	}
	parent = "rotated-enrollment-parent"
	set()
	if _, err = client.Exchange(context.Background()); err != nil {
		t.Fatal("rotation not observed", err)
	}
	for _, mode := range []string{"subject", "kind", "audience", "scope", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			subject, kind, audience, scope = "svc:test", "service", "fragments-engine", Scope
			exp = time.Now().Add(10 * time.Minute)
			switch mode {
			case "subject":
				subject = "svc:other"
			case "kind":
				kind = "operator"
			case "audience":
				audience = "other-service"
			case "scope":
				scope = "*"
			case "expiry":
				exp = time.Now().Add(-time.Minute)
			}
			if _, err = client.Exchange(context.Background()); err == nil {
				t.Fatal("accepted mismatched issuer response")
			}
		})
	}
}
func TestRedirectsNeverForwardCredentials(t *testing.T) {
	leaked := make(chan struct{}, 1)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked <- struct{}{}; w.WriteHeader(200) }))
	defer target.Close()
	issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer issuer.Close()
	resolver := secrets.New(secrets.WithStateDir(t.TempDir()))
	if err := resolver.Set(context.Background(), "file://delivery/parent", strings.NewReader("private-enrollment-parent")); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IssuerURL: issuer.URL, Subject: "svc:test", ParentRef: "file://delivery/parent"}, resolver, issuer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Exchange(context.Background()); err == nil {
		t.Fatal("accepted redirected issuer")
	}
	if _, err = client.Accept(context.Background(), issuer.URL, "private-jwt", "capture", []byte(`{}`)); err == nil {
		t.Fatal("accepted redirected capture")
	}
	select {
	case <-leaked:
		t.Fatal("forwarded credential to redirect target")
	default:
	}
}
