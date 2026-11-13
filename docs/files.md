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
↑↓ PageUp/PageDown Home/End to move, ↵ or → to open, ⌫ or ← to go up, ⌘[ ⌘] back and
forward, Space or ⌘I to toggle the preview, ⌘⇧. for hidden files, ⌃T for a terminal in this
folder (local roots), ⌘⌥C to copy the full path, ⌘R or F5 to reload.

Opening a file calls `desk.openFile`; when no app handles that file the preview shows it. The
palette finds places and recently opened files, and ⌃⌥E opens Files. The last folder, the
hidden-files and preview choices, and the recents are kept in local storage.
