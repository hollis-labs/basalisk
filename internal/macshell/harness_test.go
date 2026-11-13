//go:build darwin && desktop && harness

// An off-screen harness for the browser views: go test -tags "desktop harness" ./internal/macshell
// It runs NSApplication with the prohibited activation policy and a borderless window far off
// screen, so it never takes focus or shows anything.

package macshell

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() { runtime.LockOSThread() }

var hxBase string

func TestMain(m *testing.M) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>app</title><body style="margin:0;background:#222">
<script>window.__ev=[];window.addEventListener('basalisk:browser',e=>window.__ev.push(e.detail))</script>`)
	})
	mux.HandleFunc("/site", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Site One</title><body style="margin:0;background:rgb(200,30,30)"><a id=a href="/site2">next</a>`)
	})
	mux.HandleFunc("/site2", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Site Two</title><body style="margin:0;background:rgb(30,30,200)">two`)
	})
	srv := httptest.NewServer(mux)
	hxBase = srv.URL
	code := make(chan int)
	go func() { code <- m.Run() }()
	go func() { c := <-code; srv.Close(); os.Exit(c) }()
	HxRun()
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for end := time.Now().Add(8 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestBrowserViews(t *testing.T) {
	time.Sleep(300 * time.Millisecond)
	win := HxStart(hxBase + "/page")
	waitFor(t, "page", func() bool { return HxEvalPage("return document.title") == `["app"]` })
	HxAttach(win)
	if got := HxEvalPage("return !!window.webkit.messageHandlers.basaliskBrowser"); got != "[true]" {
		t.Fatalf("channel: %s", got)
	}
	open := HxEvalPage(fmt.Sprintf(`return await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'open',id:'w1',url:%q,frame:{x:100,y:50,w:600,h:400,visible:true}})`, hxBase+"/site"))
	t.Log("open:", open)
	waitFor(t, "load", func() bool {
		v, _ := DefaultBrowser().List()
		return len(v) == 1 && v[0].Title == "Site One" && !v[0].Loading
	})
	frame := HxViewFrame("w1")
	t.Log("frame:", frame)
	if !strings.HasPrefix(frame, "100,350,600,400 hidden=0") || !strings.HasSuffix(frame, "above=1") {
		t.Errorf("frame = %s, want 100,350,600,400 visible above the page", frame)
	}
	png, err := DefaultBrowser().Snapshot("w1")
	if err != nil || len(png) < 100 {
		t.Fatalf("snapshot: %v %d", err, len(png))
	}
	if dir := os.Getenv("HX_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/site.png", png, 0o600) //nolint:gosec // test output directory
	}
	res, err := DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": "getComputedStyle(document.body).backgroundColor"})
	if err != nil || string(res) != `"rgb(200, 30, 30)"` {
		t.Fatalf("eval: %s %v", res, err)
	}
	// Navigate through the page's channel; history and events follow.
	HxEvalPage(`return await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'navigate',id:'w1',url:location.origin+'/site2'})`)
	waitFor(t, "site2", func() bool {
		v, _ := DefaultBrowser().List()
		return len(v) == 1 && v[0].Title == "Site Two" && v[0].Back
	})
	evs := HxEvalPage(`return window.__ev.filter(e=>e.kind==='state').map(e=>e.title).filter(Boolean)`)
	t.Log("state titles:", evs)
	if !strings.Contains(evs, "Site Two") {
		t.Errorf("no state event for Site Two: %s", evs)
	}
	if _, err = DefaultBrowser().Command(map[string]any{"op": "back", "id": "w1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "back", func() bool { v, _ := DefaultBrowser().List(); return len(v) == 1 && v[0].Title == "Site One" })
	// window.open becomes an "open" event, not a window.
	_, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": "window.open('/site2'); 1"})
	waitFor(t, "open event", func() bool {
		return strings.Contains(HxEvalPage(`return window.__ev.filter(e=>e.kind==='open').map(e=>e.url)`), "/site2")
	})
	// Hidden when not visible; a cross-origin page may not drive the views.
	HxEvalPage(`return await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'frame',id:'w1',x:100,y:50,w:600,h:400,visible:false})`)
	if f := HxViewFrame("w1"); !strings.Contains(f, "hidden=1") {
		t.Errorf("not hidden: %s", f)
	}
	shot := HxEvalPage(`return (await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'snapshot',id:'w1'})).slice(0,23)`)
	t.Log("page snapshot while hidden:", shot)
	HxEvalPage(`return await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'close',id:'w1'})`)
	if v, _ := DefaultBrowser().List(); len(v) != 0 {
		t.Errorf("after close: %+v", v)
	}
}
