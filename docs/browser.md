# Browser

Real web pages in a desktop window. In the desktop app each Browser window is backed by a
native `WKWebView` that `internal/macshell` (browser_darwin.m) places over the window's body;
in a plain browser the window says the browser runs in the desktop app.

## How the page drives the views

The native layer installs a script message channel on the app's web view:

```js
await window.webkit.messageHandlers.basaliskBrowser.postMessage({ op, id, ...fields })
```

`id` is the desktop window id. Only the app's own top frame (127.0.0.1 or localhost) may use it.

| op | fields | does |
| --- | --- | --- |
| `open` | `url`, `frame?` | creates the view (once) and loads `url` |
| `frame` | `x y w h` (CSS px), `visible` | places the view over that rect, or hides it |
| `navigate` | `url` (http, https, about, data, file) | loads it |
| `back` `forward` `reload` `stop` | | |
| `focus` / `blur` | | gives the keyboard to the view / back to the app |
| `zoom` | `zoom` (0.25 to 5) | page zoom |
| `state` | | returns `{id,url,title,loading,progress,back,forward,visible}` |
| `snapshot` | | returns a JPEG data URL of the view |
| `eval` | `js` | runs script in the view, returns its result |
| `close` | | removes the view |
| `list` | | every view's state |
| `chords` | `chords` | the shortcuts to hand back while a view has the keyboard |

The views answer with `basalisk:browser` DOM events: `state` (on any change of url, title,
loading, progress or history), `error`, `crashed`, `open` (window.open, target=_blank or
⌘-click: the desktop opens another Browser window), `close`, `focus` (a click in the view, so
the desktop focuses that window), `chord` (below) and `download` (into ~/Downloads, never over a file).

The views share one persistent website data store of their own, so sites keep their logins and
never share storage with the app's origin. alert, confirm, prompt and file upload use native
sheets.

## Out of the way of the DOM

A native view draws above the page. Every frame the Browser window asks the desktop for its
geometry (`desk.geometry(winId).covered`: another window over it, the palette, the menu, the
drop-down terminal, a preview card, or the window being moved or resized) and also checks that
its body is the topmost element across a grid of points, which catches anything else in the
DOM. While either says covered, the view is hidden and a recent snapshot of it is shown in its
place.

## For agents

```
GET  /api/browser                   -> {enabled, views:[state]}
GET  /api/browser/{id}/snapshot     -> PNG of one view
POST /api/browser/{id}/{op}         -> {result}; op is navigate, back, forward, reload, stop,
                                       eval, state, focus or zoom; the body carries its fields
```

All but the list need the write scope. Placing and creating views stays with the page.

## Testing

`go test -tags "desktop harness" ./internal/macshell` runs the views in an off-screen window of
an app with the prohibited activation policy: it never takes focus or shows anything. It covers
the channel, frame placement, loading, snapshot, eval, navigation and history, the state and
open events, chord forwarding, hiding and closing.

## Shortcuts while a page has the keyboard

Keys pressed in a view go to the site, except the chords the page registered with
`{op:"chords", chords:[...]}`: every bound desktop shortcut plus the window's own (⌘L address,
⌘[ ⌘] history, ⌘R reload). The native layer swallows those and sends a `chord` event; the
window handles its own and hands the rest to the desktop as a swallowed chord, so ⌘K, ⌃\` and
the window chords work from inside a page. ⌘C, ⌘V and the rest stay with the site.

## Not yet

HTTP authentication challenges use the default handling. Opening local files (Files →
browser) needs `loadFileURL` with read access and is not wired.
