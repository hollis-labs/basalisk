package assurance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

func TestCurrentTrustWireAndFreshness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader")
	if err := tokenfile.Create(path, "tth_reader_one"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	p := auth.Principal{Verified: true, ID: "tachyon", Parent: "tachyon", Kind: "device", Audience: StatusTokenAudience, Scopes: []string{StatusTokenScope}, TokenID: "mint-one", ExpiresAt: now.Add(time.Minute)}
	var mode atomic.Int32
	var calls atomic.Int32
	var previous string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/auth/workload-trust" || r.Header.Get("Tether-Protocol") != "1" {
			t.Error("wrong issuer request")
		}
		if mode.Load() == 10 {
			if r.Header.Get("Authorization") != "Bearer tth_reader_two" {
				t.Error("rotation not observed")
			}
		} else if r.Header.Get("Authorization") != "Bearer tth_reader_one" {
			t.Error("wrong reader")
		}
		var input struct {
			QueryID  string          `json:"query_id"`
			Workload currentWorkload `json:"workload"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid query")
			return
		}
		nonce, err := base64.RawURLEncoding.Strict().DecodeString(input.QueryID)
		if err != nil || len(nonce) != 32 || input.QueryID == previous {
			t.Error("nonce not fresh")
		}
		previous = input.QueryID
		if input.Workload.TokenID != p.TokenID || input.Workload.Subject != p.ID || input.Workload.Issuer != "issuer" {
			t.Error("query lost verified identity")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		reply := map[string]any{"query_id": input.QueryID, "workload": input.Workload, "active": true, "checked_at": now}
		switch mode.Load() {
		case 1:
			reply["active"] = false
		case 2:
			reply["query_id"] = "stale"
		case 3:
			input.Workload.Parent = "other"
			reply["workload"] = input.Workload
		case 4:
			w.WriteHeader(503)
			return
		case 5:
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		case 6:
			reply["extra"] = true
		case 7:
			_, _ = w.Write([]byte(strings.Repeat(" ", 17<<10)))
			return
		case 8:
			w.Header().Del("Cache-Control")
		case 9:
			reply["checked_at"] = now.Add(time.Hour)
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	c, err := NewCurrentTrustClient(CurrentTrustConfig{server.URL, "issuer", path}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }
	for range 2 {
		if err = c.CheckCurrentWorkload(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	for i := int32(1); i <= 9; i++ {
		mode.Store(i)
		if err = c.CheckCurrentWorkload(context.Background(), p); err == nil {
			t.Fatalf("mode %d accepted", i)
		}
	}
	mode.Store(10)
	if err = tokenfile.Write(path, "tth_reader_two"); err != nil {
		t.Fatal(err)
	}
	if err = c.CheckCurrentWorkload(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	} // #nosec G302 -- intentionally unsafe fixture exercises custody refusal.
	if err = c.CheckCurrentWorkload(context.Background(), p); err == nil || calls.Load() != before {
		t.Fatal("insecure custody accepted or sent")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = c.CheckCurrentWorkload(context.Background(), p); err == nil {
		t.Fatal("removed reader reused")
	}
	if err = tokenfile.Create(path, "tth_reader_two"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.CheckCurrentWorkload(ctx, p); err == nil {
		t.Fatal("canceled trust request accepted")
	}
	p.ExpiresAt = now
	if err = c.CheckCurrentWorkload(context.Background(), p); err == nil {
		t.Fatal("expired target accepted")
	}
}
