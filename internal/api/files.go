package api

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/hollis-labs/basalisk/internal/files"
	"github.com/hollis-labs/basalisk/internal/terminal"
)

// maxWrite bounds a PUT /api/files/content body: the editor writes notes, not disk images.
const maxWrite = 64 << 20

// EnableFiles turns the file routes on. Without it GET /api/files reports the browser as
// disabled and nothing else is served: like the terminal, it reads the disk as the user who
// runs the app, so a server deployment does not get it by default.
func (a *API) EnableFiles(m *files.Manager) { a.files = m }

func (a *API) filesRoutes() []route {
	rts := []route{{"GET", "/api/files", ScopeRead, a.filesState}}
	if a.files == nil {
		return rts
	}
	r, w := ScopeRead, ScopeWrite
	rts = append(rts,
		route{"GET", "/api/files/roots", r, a.filesRoots},
		route{"GET", "/api/files/list", r, a.filesList},
		route{"GET", "/api/files/stat", r, a.filesStat},
		route{"GET", "/api/files/content", r, a.filesContent},
		route{"PUT", "/api/files/content", w, a.filesWrite},
		route{"POST", "/api/files/terminal", w, a.filesTerminal},
	) // and the operations, files_ops.go:
	return append(rts, a.filesOpsRoutes()...)
}

func (a *API) filesState(w http.ResponseWriter, _ *http.Request) {
	if a.files == nil {
		reply(w, 200, map[string]any{"enabled": false})
		return
	}
	reply(w, 200, map[string]any{"enabled": true, "roots": a.files.Roots(), "terminal": a.term != nil})
}

func (a *API) filesRoots(w http.ResponseWriter, _ *http.Request) {
	reply(w, 200, map[string]any{"roots": a.files.Roots()})
}

func fileRef(r *http.Request) (root, p string) {
	q := r.URL.Query()
	return q.Get("root"), q.Get("path")
}

func (a *API) filesList(w http.ResponseWriter, r *http.Request) {
	root, p := fileRef(r)
	list, err := a.files.List(r.Context(), root, p)
	if err != nil {
		filesFail(w, err)
		return
	}
	clean, _ := files.Clean(p)
	reply(w, 200, map[string]any{"root": root, "path": clean, "entries": list})
}

func (a *API) filesStat(w http.ResponseWriter, r *http.Request) {
	root, p := fileRef(r)
	e, err := a.files.Stat(r.Context(), root, p)
	if err != nil {
		filesFail(w, err)
		return
	}
	reply(w, 200, e)
}

// filesContent serves the bytes. http.ServeContent picks the type (by extension, then by
// sniffing), answers Range and conditional requests, and sets Last-Modified. The file is
// never run as a page of this origin: the sandbox policy gives an HTML or SVG file opened
// directly an opaque origin and no script.
func (a *API) filesContent(w http.ResponseWriter, r *http.Request) {
	root, p := fileRef(r)
	f, e, err := a.files.Open(r.Context(), root, p)
	if err != nil {
		filesFail(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	h.Set("ETag", e.ETag())
	if t, ok := textTypes[strings.ToLower(path.Ext(e.Name))]; ok {
		h.Set("Content-Type", t)
	}
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\\", "", "\n", "", "\r", "").Replace(e.Name)+`"`)
	}
	http.ServeContent(w, r, e.Name, e.Mtime, f)
}

func (a *API) filesWrite(w http.ResponseWriter, r *http.Request) {
	root, p := fileRef(r)
	body := http.MaxBytesReader(w, r.Body, maxWrite)
	e, err := a.files.Write(r.Context(), root, p, body, r.Header.Get("If-Match"))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			fail(w, 413, "The file is too large to save here.")
			return
		}
		filesFail(w, err)
		return
	}
	w.Header().Set("ETag", e.ETag())
	reply(w, 200, e)
}

// filesTerminal opens a terminal session in a local folder (a file's folder, for a file).
func (a *API) filesTerminal(w http.ResponseWriter, r *http.Request) {
	if a.term == nil {
		fail(w, 404, "This Basalisk does not hand out a shell.")
		return
	}
	var in struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := a.files.Stat(r.Context(), in.Root, in.Path)
	if err != nil {
		filesFail(w, err)
		return
	}
	rel := e.Path
	if !e.Dir {
		rel = path.Dir(rel)
	}
	dir, err := a.files.LocalPath(in.Root, rel)
	if err != nil {
		filesFail(w, err)
		return
	}
	if dir == "" {
		fail(w, 400, "A terminal opens only in a folder on this machine.")
		return
	}
	info, err := a.term.Create(terminal.Options{Cwd: dir})
	switch {
	case errors.Is(err, terminal.ErrTooMany):
		fail(w, 429, err.Error())
	case err != nil:
		fail(w, 400, err.Error())
	default:
		reply(w, 201, info)
	}
}

// textTypes are types the platform's MIME table may not know, so ServeContent would sniff
// them as plain text.
var textTypes = map[string]string{
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
}

func filesFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNoRoot), errors.Is(err, files.ErrNotFound):
		fail(w, 404, capitalize(err.Error())+".")
	case errors.Is(err, files.ErrOutside):
		fail(w, 403, "That path is outside its root.")
	case errors.Is(err, files.ErrBadPath), errors.Is(err, files.ErrNotDir), errors.Is(err, files.ErrIsDir):
		fail(w, 400, capitalize(err.Error())+".")
	case errors.Is(err, files.ErrConflict):
		fail(w, 412, "The file changed since it was read.")
	case errors.Is(err, files.ErrRemote):
		fail(w, 502, "The remote host could not be reached.")
	case errors.Is(err, fs.ErrPermission):
		fail(w, 403, "Permission denied.")
	default:
		fail(w, 500, "Could not read or write the file.")
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
