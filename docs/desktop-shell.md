# Desktop shell (macOS)

Basalisk.app is the same server `basalisk serve` runs, in one process with a native window, a
small native macOS layer and a terminal. This page describes what exists, how to build it, and
what the shell spike found. It is a spike: the code is meant to be changed.

## Build and run

```sh
make app                                  # builds build/Basalisk.app (frontend, then the binary with -tags desktop)
scripts/build-macos-app.sh --no-ui        # reuse the built frontend
scripts/build-macos-app.sh --install      # also copy to /Applications
open build/Basalisk.app
```

- The app serves on `httphost.addr` (127.0.0.1:8085 by default) and its window loads that URL. A
  browser pointed at the same address gets the same UI.
- Data is where the server keeps it (`$XDG_DATA_HOME/basalisk`, else `~/.local/share/basalisk`, or
  `BASALISK_DATA_DIR`). Pass an environment variable to a bundled app with
  `open build/Basalisk.app --env BASALISK_DATA_DIR=/some/dir`.
- Started from the bundle, stdout and stderr go to `~/Library/Logs/Basalisk/basalisk.log`, and a
  sign-in lasts 30 days instead of 12 hours.
- `basalisk desktop` runs the same thing from a desktop build outside a bundle. A build without
  the `desktop` tag (every server build) has no desktop command, no native layer and no terminal.

Signing matters. macOS attaches the Accessibility and Input Monitoring grants to the code
signature. The build script signs with `BASALISK_SIGN_IDENTITY`, else the first "Developer ID
Application" identity in the keychain, else ad-hoc. An ad-hoc signature changes on every build
and loses the grants each time.

## Layout

| Piece | Where |
| --- | --- |
| Desktop command: serve in-process, Wails v3 window on the loopback URL | `cmd/basalisk/desktop_darwin.go` |
| Platform adapter interface (`Controller`) and shortcut parsing | `internal/macshell/macshell.go` |
| macOS implementation: cgo and Objective-C | `internal/macshell/shell_darwin.{go,h,m}` |
| Terminal sessions (PTY, replay, fan-out) | `internal/terminal` |
| HTTP routes | `internal/api/shell.go`, `internal/api/terminal.go` |
| Terminal UI (one deck, moved between the page and the overlay) | `frontend/src/terminal` |
| Pages | `frontend/src/pages/terminal.tsx`, `frontend/src/pages/shell.tsx` |

The window uses no Wails asset server and no Wails bindings. The page talks to the native layer
through the ordinary API, so nothing in the frontend depends on Wails:

- `GET /api/shell` and `GET /api/terminal` are always served and say whether the feature is there.
  Everything else under them exists only in the desktop app.
- The shell tells the page about each event with a DOM event, `basalisk:shell`.

### The desktop

Signed in, the app is a desktop: a bar, an empty canvas, and windows (`frontend/src/desktop`).

- **Bar.** Menu (Apps, Settings, layout, kiosk, sign out), one chip per open window, the
  inbox with its unread count (docs/inbox.md), the layout toggle, two shell toggles (own the
  keyboard; kiosk, which blocks Cmd-Tab), the clock. The windows that are open are kept as they
  change and reopened at the next start, after a restart, a shutdown or a crash. In full screen on a display with a camera housing the
  bar sits in the strip beside it and keeps its middle empty.
- **Window manager** (`wm.ts`, a reducer and a pure layout function; no library). Two layouts:
  *tile* arranges windows as a master on the left and a stack on the right, with a draggable
  divider, and a window can be floated out of it; *float* leaves every window where you put
  it. Drag a tiled window onto another to trade places. Keys, all with ⌃⌥: `T` layout,
  `J`/`K` next/previous, `Enter` maximize, `F` float or re-tile, `M` make master, `H` hide,
  `W` close, `←`/`→` resize the master (tile) or snap to a half (float), `↑` fill, `↓` centre.
- **Apps** are entries in a registry (`registry.ts`): id, title, icon, default size, a render
  function that receives the window id and the arguments it was opened with. An app marked
  `multi` can have several windows, told apart by a key. Settings is an app with sections
  (Appearance, Shortcuts, Account, Security, Integrations, Devices, Mac shell).
- **Commands** are named actions (`commands.ts` and each app's own). Everything the desktop
  does is one, plus "Open <app>" for every app. They fill the palette and carry the shortcuts.
- **Command palette** (`palette.tsx`, ⌘K): commands, apps, open windows, and rows from
  registered providers (files, notes, cited records), fuzzy matched, most recent first.
- **Shortcuts** (`keys.ts`, `shortcuts.tsx`). A shortcut belongs to a command. Defaults live
  in the commands; Settings, Shortcuts records a new one, clears one or resets it, and a
  chord given to a second command is taken from the first. Chords also arrive from the
  native event tap: with key swallowing on, a swallowed chord (⌘Space, say) runs whatever
  command it is bound to.
- **Desk settings** (`settings.ts`, `GET`/`PUT /api/desk/settings`): theme, layout and
  shortcuts, one JSON document per person kept beside the database, cached in local storage.
- **Themes** (`themes.ts`). Palettes taken from four Omarchy themes (Aura, the default;
  Moodpeak; Cyberphunk; Blade Rainner), applied by overriding the design tokens' base
  variables, so kit components and the terminal follow. Chosen in Settings, Appearance.
- The desktop starts empty every launch; the layout, theme and shortcuts are remembered.
- The app-shell `ManifestShell` is no longer rendered and `cmd/basalisk/app.ui.json` no longer
  drives navigation. The desktop's app registry should eventually be fed by the manifest.

### Cite and Notes

A reference in any text becomes a chip with a preview card: a Torque id (`CW-…`, `EP-…`,
`PRJ-…`), a Tesseract item (`tesseract:<item_id>`, a bare item id, `tesseract:<namespace>#<key>`),
a `[label](tach:<service>/<id>)` link, a URL. The syntax is the one the Tether skill
`cite-references` teaches agents, so what an agent writes is what resolves here.

- **Detection** is `frontend/src/cite/core`, lifted unchanged from Tachyon's console (the
  kind registry, the scanner, the batching resolver cache). Text in code spans is left alone.
- **Resolution** is `GET /api/cite/resolve` and `/api/cite/record` (`internal/api/cite.go`).
  Every kind is projected onto one entity shape (title, status, a few fields, a markdown
  body), so there is one card and one viewer. Records are read through a Tether MCP gateway
  (`internal/tetherlink`): a child process on stdio, the same command an agent session is
  launched with, named by `basalisk.tether_mcp` in the config file. No token is stored in
  Basalisk. Without that setting references are still detected, with no preview.
- **Where it shows:** `<CiteText>` and `<Markdown>` (`frontend/src/cite/ui.tsx`) for any
  app's text, the terminal (a link provider: hover a task id in output for its card, click
  to open it), and the palette (type or paste an id).
- **Notes** (`frontend/src/apps/notes`) is the viewer: a markdown or text file through the
  file API, or a cited record, rendered with its references live. It registers as the
  handler for `.md`, `.txt`, `.log` and similar, so `desk.openFile` lands there. A file can
  be edited in place (a cited record cannot): source on the left and, for markdown in a wide
  window, the rendered page on the right. ⌘S saves with `If-Match`, so a file that changed
  on disk since it was read is not overwritten without asking. An unsaved edit is kept in
  local storage with the version it started from, and is restored when the file is opened again.
- The desktop app reads `$XDG_CONFIG_HOME/basalisk/basalisk.yaml` (else
  `~/.config/basalisk/basalisk.yaml`) when `SUBSTRATE_CONFIG` is not set.
- `window.basalisk.desk` is the desktop API, for an agent driving the app through the
  shell's eval endpoint.

To add a kind: a `RefKind` in the frontend registry (`cite/index.ts`) for detection, and an
entry in `citeKinds` in `internal/api/cite.go` for the record behind it.

#### Adding an app

Put it in `frontend/src/apps/<name>/` and import it from `frontend/src/apps/index.ts`. The
module registers what it has when imported:

```ts
registerApp({ id: "files", title: "Files", icon: Folder, menu: true, size: [1000, 640], bare: true,
  render: ({ winId, args }) => <Files winId={winId} args={args} /> })
registerCommands([{ id: "files.home", title: "Files: go home", section: "Files", keys: "ctrl+alt+e",
  run: desk => desk.open("files", { args: { path: "~" } }) }])
registerProvider({ id: "files", search: async (q, desk) => [/* rows whose ids start "files:" */] })
registerFileHandler({ app: "notes", match: name => /\.(md|txt)$/.test(name) })
registerOverlay({ id: "frag", render: ({ close }) => <Capture onDone={close} /> })   // desk.toggleOverlay("frag")
```

An overlay is drawn over the whole desktop instead of in a window. The desktop mounts it
while it is open, treats every window as covered, and returns the keyboard to the focused
window when it closes; the overlay draws its own backdrop and handles its own keys.

Inside a window, `useDesk()` gives the desktop: `open(app, { args, key, title })`,
`openFile({ root, path })` (opens the app registered for that file), `close`, `setTitle`,
the window manager, the palette, the terminal toggle, the shell status, and
`setDirty(winId, dirty)` (while set, closing the window asks first),
`context(winId?)` (what a window is showing: app, title, and the file, page, folder and
selection as far as its app can say; an app adds to it with `registerContext`), and
`confirm({ title, message, confirmLabel, danger })`, a yes/no dialog that resolves to a
boolean. Use it instead of `window.confirm`: the desktop app's web view has no handler for
JavaScript dialogs and answers "no" without showing anything. For content that is
not DOM (a native web view over a window), `geometry(winId)` gives the window's rectangle on
screen and `covered`, true while another window, the palette, the menu, the drop-down
terminal or a preview card is over it or it is being moved; `subscribe(fn)` calls back when
any of that may have changed. Backend routes go in
their own file in `internal/api` and are added to `allRoutes` in `api.go`.

The desktop's shortcuts run before the window sees the key (a capture listener on the
window). An element can keep chords for itself with `data-keys-own="cmd+k cmd+b"`: while focus
is inside it, the desktop leaves those chords alone and the element's own `onKeyDown` gets
them. Chords are spelled as `chordOf` makes them (`ctrl`, `alt`, `shift`, `cmd`, then the key, e.g.
`shift+cmd+g`). Notes' editor uses it to keep ⌘B, ⌘I, ⌘K, ⌘F and ⌘S. Two rules keep it
predictable: an element keeps only chords it actually handles, and only a text editor may
keep the palette toggle. Chords posted from the native event tap ("Own the keyboard") arrive
as ordinary key events, so the attribute applies to them too.

### The adapter

```go
type Controller interface {
    Status() Status
    SetKiosk(on, blockSwitching bool) error
    SetMenuBar(visible bool) error
    SetSwallow(on bool, combos []string) error
    Windows() ([]Window, error)
    Unminimize(pid, index int) error
    SetFixMinimized(on bool) error
    RequestPermission(kind string) error
    Events(after int64) []Event
    Snapshot() ([]byte, error)                 // the page as a PNG, no screen recording permission
    Eval(js string) (json.RawMessage, error)   // run script in the page
}
```

`macshell.Default()` is the macOS implementation in a desktop build and `nil` everywhere else. A
Linux adapter would implement the same interface.

### Fixed shortcuts

| Keys | Effect |
| --- | --- |
| ⌃⌥⌘ Esc | Leave kiosk and stop key swallowing. Handled before anything else; cannot be swallowed. |
| ⌃⌥⌘ F | Toggle kiosk (Cmd-Tab blocked while on). Works from any app (it brings Basalisk forward). |
| ⌃⌥⌘ M | Show or hide the menu bar while in kiosk. |
| ⌃⌥⌘ J | Summon the agent from any app: Basalisk comes forward and the agent's panel opens with the keyboard in it. |
| ⌃ ` | Drop the terminal down over any page, or put it away. |

The first four are Carbon hot keys and need no permission. Force Quit (⌘⌥ Esc) is never
swallowed and never disabled.

### Terminal

Sessions live in the server, not the page: reloading, navigating or closing the overlay leaves
the shells running, and a new attach replays the last 512 KiB of output before going live. The
wire protocol is Tachyon's chat terminal protocol (binary frames are raw bytes both ways; text
frames are JSON `resize` out, `state` and `exit` in). Each shell is the login shell (`$SHELL -l`)
in the home directory with `TERM=xterm-256color`. Sessions end when the app quits.

In the terminal: ⌘T new tab, ⌘W close, ⌘1-9 and ⌘⇧[ ] switch, ⌘K clear, ⌘+ ⌘- ⌘0 font size,
⌥←/→ word, ⌘←/→ line start and end, ⌘⌫ kill line, ⇧Enter newline.

**Terminal windows.** The Terminal window (and the drop-down) is the deck: one window with
tabs. A shell can also have a window to itself, so several can be tiled side by side
(`frontend/src/terminal/solo.tsx`): the command "Terminal: new window", the button on a tab
("Move to its own window"), or the agent's `terminal_open` with `own_window`. A shell in a
window of its own is not a tab in the deck. There ⌘T opens another such window and ⌘W
closes it; closing the window ends the shell, and the shell ending closes the window. A
page reload brings these shells back as tabs in the deck, since windows are not restored.

Every terminal route needs the write scope, the socket included, and the socket refuses another
origin. The terminal is a shell as the user who runs the app: anyone who can sign in to this
Basalisk can run commands as that user.

### For agents: seeing and driving the real window

`GET /api/shell/snapshot` returns the page as the window shows it, as a PNG. With the app
started with `BASALISK_SHELL_DEBUG=1`, `POST /api/shell/eval` (`{"js": "..."}`) runs script in
the page and returns its result. Together they let an agent check a change in the real WebKit
view without screen recording permission or a person at the keyboard.

To do that while the owner is using the machine, build a separate copy and run it in the
background, where it has no Dock icon, is never brought to the front, and keeps its window
off screen (the page still renders and can be snapshotted):

```sh
BASALISK_BUNDLE_ID=com.hollislabs.basalisk.dev BASALISK_APP_NAME="Basalisk Dev" scripts/build-macos-app.sh --no-ui
open -g -n "build/Basalisk Dev.app" --env BASALISK_BACKGROUND=1 --env BASALISK_SHELL_DEBUG=1 \
  --env BASALISK_DATA_DIR=/some/scratch --env SUBSTRATE_CONFIG=/some/dev.yaml
```

Without `BASALISK_BACKGROUND=1` a regular app is activated when it starts, whatever `open -g`
says. The separate bundle id gives the copy its own web view storage, so it does not share
the installed app's sign-in.

## Spike findings (2026-11-12, macOS 15.7.9, Apple silicon, one built-in display with a camera housing)

"Ran" means the step was executed here and the result observed. "Not yet" means the code is in
place and the step needs a person at the keyboard.

1. **Borderless full screen with the menu bar and Dock hidden.** Ran: with
   `HideDock | HideMenuBar` set, the window made borderless and given the screen frame, the web
   view rendered at the full 3024 × 1964 pixels and the terminal refitted. Leaving restored the
   previous frame and style. No separate Space and no animation, as intended.
   `AutoHideDock` (menu bar visible) and each mode with `DisableProcessSwitching` were all
   accepted by AppKit without an exception. Not yet: looking at it. The page is pushed below
   the 32pt camera strip while the menu bar is hidden (`html.kiosk`); whether that looks right
   needs eyes.
2. **Cmd-Tab blocked while frontmost.** Not yet. `DisableProcessSwitching` is set without
   error; whether Cmd-Tab is dead, and whether the escape chord then works, needs the keys.
3. **Keys reach the page in kiosk; an event tap swallows chosen shortcuts.** Not yet. The
   Wails window class answers `canBecomeKeyWindow` YES, which is what a borderless window needs,
   and the web view is made first responder after the style change. The tap is written and
   compiles; it could not be created here because the app has no Accessibility grant
   (`AXIsProcessTrusted` false, `CGPreflightListenEventAccess` false on first run). The Mac
   shell page shows the keys the page receives and the keys the tap took.
4. **List windows and restore a minimized one.** Not yet (needs the Accessibility grant). The
   calls are `kAXWindowsAttribute`, then `kAXMinimizedAttribute = false` and `kAXRaiseAction`.
   AltTab (GPL-3.0) restores a minimized window with the same Accessibility call when you pick
   it, then fronts it with a private SkyLight call. So AltTab already handles this in its own
   switcher; what does not is the system Cmd-Tab, which activates the app and leaves every
   window in the Dock. The shell has a switch for exactly that ("Restore a minimized window when
   its app is activated"), which makes a fork unnecessary for this complaint.
5. **Second display.** Not run: one display was connected.

Other things learned:

- **Where the tap has to sit (2026-11-13, reported at the keys).** With the tap at the
  session level and swallowing on, another app's global hot key (Raycast on Control-Space)
  still fired: global hot keys are matched after events enter the window server and before
  they reach the session. The tap is now created at the HID level, falling back to the
  session level, and the Mac shell page says which it got. Owning the keyboard also changed
  from a list of chords to a rule: while on, any key carrying Command, Control or Option, and
  any function key, is taken from the shared stream and posted to this process alone
  (`CGEventPostToPid`), so no other hot key can match it and the page still receives it. The
  list that remains names the chords also kept from the app itself (quit, hide, minimize).
  Not yet confirmed at the keys.

- **cgo in process: go.** One binary, one signature for the grants, no socket protocol. The
  Objective-C is about 550 lines beside the Go that wraps it. A Swift helper would only be
  worth it if the tap had to outlive the app.
- **Wails.** v3.1.0 is the current stable release; there is no v4 module. Tangent's shell
  (v3.0.0-beta.26) was removed from its main branch on 2026-11-12, so there is no shared shell
  code to follow. Wails' `ExecJS` does nothing for a page loaded from the loopback server (it
  waits for a Wails runtime the page never loads), so the shell evaluates script itself.
- **Activation.** `activateIgnoringOtherApps` brought the app forward from the background.
- **Fonts.** The web view can use fonts installed for the user (a Powerline font resolved).
- **Retina and WebGL.** The xterm WebGL renderer is correct in the web view at scale 2.
  Headless Chrome with an emulated scale factor draws it at double size; that is the emulation,
  not the app. Check terminal rendering with `/api/shell/snapshot`, not a headless browser.
- **Safety.** The tap runs on its own thread, acts only while the app is frontmost, stops
  swallowing when the main thread has not answered for 3 seconds, and is removed when
  swallowing is turned off. Presentation options are per app, so they end with the process.
- **Sign-in.** The web view keeps its cookies across launches.

## Known gaps

- No app icon.
- Tether agents and file paths are detected as references but have no preview.
- Notes edits in a plain text area (no syntax highlighting, no markdown shortcuts) and cannot
  create a new file.
- One window per app: two terminals side by side need a second terminal window, which the
  deck does not support yet (tabs only).
- The terminal has no search, no split panes and no configuration; font size is the one setting
  (kept in the web view's local storage).
- Windows on other Spaces are not listed: the Accessibility API only returns the current Space.
- `substrate pins check` knows nothing of the three new Go dependencies (Wails, creack/pty,
  coder/websocket); they are not in the Substrate BOM.
