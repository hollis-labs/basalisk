# Files

The file browser serves a few allowlisted folders ("roots") through `/api/files`. It is on in
the desktop app and, for a plain `basalisk serve`, only with `BASALISK_FILES=1`: like the
terminal, it reads and writes the disk as the user who runs the server.

## Roots

- **Local:** by default `~`, `~/dev`, `~/Downloads`, `~/Documents` and `~/Projects`, those
  that exist. Every access goes through an `os.Root`, so a path or a symbolic link that leads
  outside its root is refused (a link like that is listed, marked `broken`).
- **Remote (kind `sftp`):** a folder on another host, reached by running the system `ssh`
  with the host's `sftp` subsystem, so the user's ssh config, keys, agent and jump hosts all
  apply. Nothing is mounted. Paths are resolved on the host (`realpath`) and refused when
  they lead outside the root. The connection opens on first use and is reopened once if it
  drops.

Configure them in `files.json` beside the database (or the file named by
`BASALISK_FILES_CONFIG`); see [files.example.json](files.example.json). A `local` list
replaces the defaults. `BASALISK_FILES_REMOTES=host:path,...` adds remotes from the
environment. A bad config is logged and leaves the browser off.

## API

Paths are relative to the root, slash separated, `""` for the root itself.

```
GET /api/files                             -> {enabled, roots, terminal}   (always served)
GET /api/files/roots                       -> {roots:[{id,label,kind:"local"|"sftp",host?,path}]}
GET /api/files/list?root=ID&path=REL       -> {root,path,entries:[Entry]}  (folders first, then by name)
GET /api/files/stat?root=ID&path=REL       -> Entry
GET /api/files/content?root=ID&path=REL    -> the bytes (Range, ETag, Last-Modified; &download=1 to save)
PUT /api/files/content?root=ID&path=REL    -> Entry. Body is the new content (64 MiB at most).
                                              If-Match: <ETag> refuses with 412 when the file changed.
POST /api/files/terminal {root,path}       -> a terminal session in that folder (a file's folder); local roots only

Entry: {name, path, dir, size, mtime (RFC 3339), mode ("-rw-r--r--"), symlink, broken?}
```

Changes (write scope); nothing is ever overwritten, a taken name is 409:

```
POST   /api/files/mkdir   {root, path}                      -> 201 Entry   new folder
POST   /api/files/create  {root, path}                      -> 201 Entry   new empty file
POST   /api/files/move    {from: {root, path}, to: {...}}   -> Entry       rename, or move (also between places)
POST   /api/files/copy    {from, to}                        -> 201 Entry   folders with their contents
POST   /api/files/delete  {root, path}                      -> {trashed}   local: to the Trash; remote: for good
GET    /api/files/search?root=&path=&q=                     -> {entries, truncated}
GET    /api/files/grep?root=&path=&q=                       -> {matches: [{path, line, text}], truncated, engine}
GET    /api/files/hosts                                     -> {hosts}     concrete Host names in ~/.ssh/config
POST   /api/files/roots   {host, path, label?}              -> 201 Root    add a remote place (kept in files.json)
DELETE /api/files/roots/{id}                                -> 204         remove a remote place (its files untouched)
```

A move or copy between places copies through the two places' own reads and writes; a move then
removes the original (to the Trash when it was local). A folder cannot move or be copied into
itself. Local delete moves the item into `~/.Trash` (renamed if the name is taken there) and only
when its real folder is inside the root; a link is moved itself, never its target. Search
matches every word of `q` in names, case-insensitive, skips `.git` and `node_modules` unless it
starts inside one, and stops at 200 results, 200,000 items or 5 seconds (`truncated`). A copy
stops at 20,000 items (413).

Errors: 404 no such root or file, 403 outside the root, 400 not a folder / is a folder / bad
path, 412 changed since read, 502 remote host unreachable. Reads need the read scope; writes
and the terminal need the write scope.

`/api/files/content` sets the type from the extension (Markdown as `text/markdown`), then by
sniffing, and always sends `Content-Security-Policy: sandbox`, so an HTML or SVG file opened
directly runs no script in this origin. A write replaces the file atomically (a temporary file
beside it, then a rename) and keeps its permissions; a symbolic link is written through and
stays a link.

Remote modification times have one-second resolution, so two saves of the same size within
one second share an ETag.

## The Files window

`frontend/src/apps/files`. Places (roots) on the left, the folder in the middle, a preview on
the right (text, images, audio, video, file details). It is keyboard first: type to filter,
↑↓ PageUp/PageDown Home/End to move, ⌘O or → to open, ⌫ or ← to go up, ⌘[ ⌘] back and
forward, Space or ⌘I to toggle the preview, ⌘⇧. for hidden files, ⌃T for a terminal in this
folder (local roots), ⌘⌥C to copy the full path, ⌘R or F5 to reload.

Managing files: ⌘N new file and ⌘⇧N new folder (the name is typed in place), Enter or F2 renames,
⌘D duplicates, ⌘⌫ deletes (to the Trash locally; a remote delete asks first and says it is
permanent), ⌘C / ⌘X / ⌘V copy, cut and paste files between folders and Files windows, ⌘A selects
everything; ⌘-click and ⇧-click or ⇧↑↓ select several. A right-click gives the same as a menu.
Rows drag onto a folder or a place to move them (⌥ to copy). A name that is taken is never
replaced: Files asks, and "keep both" gives the new one a free name. Every window showing a
changed folder reloads it. ⌘O, ⌘↓, → (folders) or a double-click opens.

Searching: ⌘F in a Files window searches under the folder it shows, by name or (Tab) by
contents, and the results replace the listing until Esc; Enter opens one. Content search runs
ripgrep where the place has it (on this machine, or on the remote host over ssh, every argument
quoted for the remote shell); without it a local place is scanned here (text files up to 2 MiB,
.git and node_modules skipped) and a remote one says ripgrep is missing. It is literal and
case-insensitive, 5 lines per file and 200 in all, 8 seconds at most. ⌘K finds files by name in
the place last open in Files.

Opening a file calls `desk.openFile`; when no app handles that file the preview shows it. The
palette finds places and recently opened files, and ⌃⌥E opens Files. The last folder, the
hidden-files and preview choices, and the recents are kept in local storage.

Window args: `{root?, path?, select?}`. `select` is a name in that folder (`"notes.md"`) or a
root-relative path; the window opens with it highlighted. Without `root` the window opens at
the last folder it showed.
