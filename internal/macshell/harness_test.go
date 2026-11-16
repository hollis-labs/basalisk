//go:build darwin && desktop && harness

// An off-screen harness for the browser views: go test -tags "desktop harness" ./internal/macshell
// It runs NSApplication with the prohibited activation policy and a borderless window far off
// screen, so it never takes focus or shows anything.

package macshell

import (
	"encoding/json"
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
	// Exercise the real browser CSS in WebKit, including the bare desktop flex parent.
	css, err := os.ReadFile("../../frontend/src/apps/browser/browser.css")
	if err != nil {
		t.Fatal(err)
	}
	layout := HxEvalPage(fmt.Sprintf(`
const style=document.createElement('style');style.textContent=%q;document.head.append(style);
const host=document.createElement('div');host.style.cssText='display:flex;width:600px;height:400px';
host.innerHTML='<div class="browser-tabs-window"><div class="browser-tab-page" style="display:flex"><div class="browser"><div class="browser-bar">address</div><div class="browser-stage"><div class="browser-slot"></div></div></div></div></div>';
document.body.append(host);
const slot=host.querySelector('.browser-slot');
const before=slot.getBoundingClientRect();host.style.width='1000px';host.style.height='700px';
const after=slot.getBoundingClientRect();
const result=[before.width,after.width,Math.round(after.height-before.height)];
host.remove();style.remove();return result`, string(css)))
	if layout != `[[600,1000,300]]` {
		t.Fatalf("browser slot did not fill/rescale with its window: %s", layout)
	}
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
	// ⌘-click on the link (top left of the view, which sits at 100,350 to 700,750 in the
	// window) becomes an "open" event and leaves the page where it is. Middle-click takes the
	// same path (buttonNumber 2) but cannot be synthesized here; see hxClick.
	countOpens := func() string { return HxEvalPage(`return window.__ev.filter(e=>e.kind==='open').length`) }
	before := countOpens()
	HxClick(112, 742, true)
	waitFor(t, "cmd-click open", func() bool { return countOpens() != before })
	if got := HxEvalPage(`return window.__ev.filter(e=>e.kind==='open').at(-1).background`); got != `[true]` {
		t.Errorf("modified link should open in background: %s", got)
	}
	if v, _ := DefaultBrowser().List(); len(v) != 1 || v[0].Title != "Site One" {
		t.Errorf("the page moved: %+v", v)
	}

	before = countOpens()
	HxControlClick(112, 742)
	waitFor(t, "control-click open", func() bool { return countOpens() != before })
	if v, _ := DefaultBrowser().List(); len(v) != 1 || v[0].Title != "Site One" {
		t.Errorf("control-click navigated parent: %+v", v)
	}

	// A popup retains its actual WebKit view and opener, rather than replaying its URL.
	_, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": "window.popup = window.open('/site2'); !!window.popup"})
	var popupID string
	waitFor(t, "popup view", func() bool {
		views, _ := DefaultBrowser().List()
		for _, v := range views {
			if v.ID != "w1" && v.Title == "Site Two" {
				popupID = v.ID
				return true
			}
		}
		return false
	})
	result, err := DefaultBrowser().Command(map[string]any{"op": "eval", "id": popupID, "js": "window.opener.document.title = 'Callback received'; !!window.opener"})
	if err != nil || string(result) != "true" {
		t.Fatalf("popup opener: %s %v", result, err)
	}
	waitFor(t, "opener callback", func() bool {
		views, _ := DefaultBrowser().List()
		for _, v := range views {
			if v.ID == "w1" {
				return v.Title == "Callback received"
			}
		}
		return false
	})
	// Adopting the view must not replay the request or destroy its JS state.
	_, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": popupID, "js": "window.retained = 42"})
	_, _ = DefaultBrowser().Command(map[string]any{"op": "open", "id": popupID, "url": hxBase + "/site"})
	result, err = DefaultBrowser().Command(map[string]any{"op": "eval", "id": popupID, "js": "window.retained"})
	if err != nil || string(result) != "42" {
		t.Fatalf("popup adoption: %s %v", result, err)
	}
	_, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": popupID, "js": "window.close(); 1"})
	waitFor(t, "popup close event", func() bool {
		return strings.Contains(HxEvalPage(`return window.__ev.filter(e=>e.kind==='close').map(e=>e.id)`), popupID)
	})
	_, _ = DefaultBrowser().Command(map[string]any{"op": "close", "id": popupID})
	_, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": "document.title = 'Site One'"})
	// Agent commands are logged where the desktop shows them.
	var logged bool
	for _, e := range shell.Events(0) {
		logged = logged || (e.Kind == "browser" && strings.Contains(e.Detail, "agent eval w1: getComputedStyle"))
	}
	if !logged {
		t.Error("the agent's eval is not in the shell log")
	}

	// Find in page: a word on the page is found, a missing one is not.
	found, err := DefaultBrowser().Command(map[string]any{"op": "find", "id": "w1", "query": "next"})
	if err != nil || string(found) != `{"found":true}` {
		t.Errorf("find next = %s %v", found, err)
	}
	if found, _ = DefaultBrowser().Command(map[string]any{"op": "find", "id": "w1", "query": "zzqx"}); string(found) != `{"found":false}` {
		t.Errorf("find zzqx = %s", found)
	}
	if _, err = DefaultBrowser().Command(map[string]any{"op": "find-clear", "id": "w1"}); err != nil {
		t.Error(err)
	}

	// Frag's Clip: fe-clipper's extraction bundle runs in the view and reports the page.
	if bundle, err := os.ReadFile("../../frontend/public/clip/extract.js"); err == nil {
		if _, err = DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": string(bundle) + "\n;0"}); err != nil {
			t.Fatal("clip bundle:", err)
		}
		var raw json.RawMessage
		waitFor(t, "clip capture", func() bool {
			raw, _ = DefaultBrowser().Command(map[string]any{"op": "eval", "id": "w1", "js": "(()=>{const c=window.__FE_CLIPPER_CAPTURE__;return c?JSON.stringify(c):null})()"})
			return len(raw) > 4 && string(raw) != "null"
		})
		var text string
		_ = json.Unmarshal(raw, &text)
		var c struct {
			Identity struct{ SubmittedURL string } `json:"identity"`
			Document struct{ Title string }        `json:"document"`
		}
		if json.Unmarshal([]byte(text), &c) != nil || c.Document.Title != "Site One" || !strings.HasSuffix(c.Identity.SubmittedURL, "/site") {
			t.Fatalf("clip capture: %s", text)
		}
	}

	// A local file opens with read access to its folder.
	dir := t.TempDir()
	if err = os.WriteFile(dir+"/local.html", []byte(`<title>Local File</title>hi`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = DefaultBrowser().Command(map[string]any{"op": "navigate", "id": "w1", "url": "file://" + dir + "/local.html"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "file", func() bool { v, _ := DefaultBrowser().List(); return len(v) == 1 && v[0].Title == "Local File" })
	// "Open in default browser" is for web pages only. (It is never run on one here: that
	// would open the default browser and take focus.)
	if _, err = DefaultBrowser().Command(map[string]any{"op": "external", "id": "w1"}); err == nil {
		t.Error("external opened a file page")
	}

	// App chords reach the shell even with native page focus, before AppKit can close it.
	HxEvalPage(`window.__shell=[];window.addEventListener('basalisk:shell',e=>window.__shell.push(e.detail.detail));return true`)
	HxEvalPage(`return await window.webkit.messageHandlers.basaliskBrowser.postMessage({op:'focus',id:'w1'})`)
	HxKey(40, true, "k")
	HxKey(17, true, "t")
	HxKey(13, true, "w")
	HxKey(8, true, "c") // Copy remains with the site.
	waitFor(t, "shell chords", func() bool {
		return HxEvalPage(`return window.__shell`) == `[["cmd+k","cmd+t","cmd+w"]]`
	})
	if v, _ := DefaultBrowser().List(); len(v) != 1 {
		t.Errorf("app chord closed the native view: %+v", v)
	}

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
