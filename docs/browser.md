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
| `navigate` | `url` (http, https, about, data, file) | loads it (a file with read access to its folder) |
| `back` `forward` `reload` `stop` | | |
| `focus` / `blur` | | gives the keyboard to the view / back to the app |
| `zoom` | `zoom` (0.25 to 5) | page zoom |
| `find` | `query`, `backwards?`, `caseSensitive?` | finds and selects the next match (wraps); returns `{found}` |
| `find-clear` | | clears the selection |
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

All but the list need the write scope, and the routes exist only in the desktop app (never on
a plain `basalisk serve`). Placing and creating views stays with the page. Every command an API
client runs (navigate with its URL, eval with the first 200 characters of its script, and the
rest) is written to the shell event log, so what an agent did in pages you are signed in to is
visible in Settings, Mac shell.

## Testing

`go test -tags "desktop harness" ./internal/macshell` runs the views in an off-screen window of
an app with the prohibited activation policy: it never takes focus or shows anything. It covers
the channel, frame placement, loading, a local file, snapshot, eval, navigation and history, the state and
open events, chord forwarding, hiding and closing.

## Shortcuts while a page has the keyboard

Keys pressed in a view go to the site, except the chords the page registered with
`{op:"chords", chords:[...]}`: every bound desktop shortcut plus the window's own (⌘L address,
⌘[ ⌘] history, ⌘R reload). The native layer swallows those and sends a `chord` event; the
window handles its own (⌘L, ⌘F, ⌘G, ⇧⌘G, ⌘[ ⌘], ⌘R) and hands the rest to the desktop as a swallowed chord, so ⌘K, ⌃\` and
the window chords work from inside a page. ⌘C, ⌘V and the rest stay with the site.

## Find in page

⌘F opens a find bar under the address field; it searches as you type. Enter or ⌘G finds the
next match, ⇧Enter or ⇧⌘G the previous one (wrapping), Esc closes the bar, clears the selection
and gives the keyboard back to the page. A field in the chrome (address, find) takes the
keyboard by first asking the view to give it up (`blur`), since DOM focus alone leaves keys
going to the native view.

## Zoom

⌘+ and ⌘- step through Safari's zoom levels, ⌘0 resets. The level is remembered per site host
and applied whenever a window lands on that host; a badge in the chrome shows it when it is not
100% (click to reset).

## The browser's data

Per person, in a private file beside the desk settings (`<owner>.browser.json`, mode 0600),
never logged:

```
GET    /api/browser/prefs              -> {zoom: {host: level}}
PUT    /api/browser/prefs/zoom         {host, zoom}   (1 forgets the host)
GET    /api/browser/history?q=&limit=  -> {entries: [{url, title, last, count}]}
POST   /api/browser/history            {url, title}   (http and https only, no fragment)
DELETE /api/browser/history[?url=]     one page, or everything
```

History keeps the 5000 most recent pages. A visit to the same page within a minute updates its
title without counting again. With a query, pages whose address or title hold every word are
ranked by visits weighted for recency, and a host that starts with the query comes first.

## History

Every page a window settles on is recorded (http and https only). Typing in the address field
lists matching pages under it (↓ ↑ to choose, Enter to go, Esc to close the list), and the
palette's History section finds them too (rows `browser:history:<url>`); both rank as above.

## Links stay in the desktop

In the desktop app, web links open in a Browser window: cite URL chips, links in rendered
Markdown, the palette's "Open <url>", and URLs clicked in the terminal (`openLink` in
`api/browser.ts`; a new tab anywhere else). A Browser window keeps its `args.url` on the page it
shows (`desk.setArgs`), so a restored desktop can reopen it there.

## Files and sign-in

In the desktop app the Browser is the file handler for `.html`, `.htm`, `.xhtml`, `.pdf` and
`.svg`: opening one in Files (a local root) shows it here as a `file://` URL, loaded with read
access to its own folder only. Files on a remote root keep Files' preview. HTTP basic and digest
sign-in use a sheet; credentials last for the session.
