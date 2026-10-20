package dataread

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// Call is the common dispatch for HTTP and future MCP tool registration. The
// caller identity comes exclusively from authenticated context, never args.
// This is not an MCP server or an authentication middleware.
func (h *Host) Call(ctx context.Context, method string, args []byte) (any, error) {
	if len(args) > 16384 {
		return nil, ErrLimit
	}
	decode := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(args))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
			return ErrInvalid
		}
		return nil
	}
	switch method {
	case "data.discover":
		var in struct{}
		if err := decode(&in); err != nil {
			return nil, err
		}
		return h.Discover(ctx)
	case "data.get":
		var in GetRequest
		if err := decode(&in); err != nil {
			return nil, err
		}
		return h.Get(ctx, in)
	case "data.query":
		var in QueryRequest
		if err := decode(&in); err != nil {
			return nil, err
		}
		return h.Query(ctx, in)
	default:
		return nil, ErrUnsupported
	}
}

// Handler requires a verified principal stamped by upstream middleware. It is
// intentionally not mounted in Basalisk's live mux until enrollment and caller
// policy are available. MCP registration should call Call with the same context.
func (h *Host) Handler() http.Handler {
	m := http.NewServeMux()
	for _, name := range []string{"discover", "get", "query"} {
		m.HandleFunc("POST /api/data/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
			var out any
			if err != nil {
				err = ErrLimit
			} else {
				out, err = h.Call(r.Context(), "data."+name, b)
			}
			if err != nil {
				err = SafeError(err)
				status := 503
				switch err {
				case ErrAccess:
					status = 403
				case ErrInvalid, ErrUnsupported:
					status = 400
				case ErrCursor, ErrVersion, ErrChanged:
					status = 409
				case ErrNotFound:
					status = 404
				case ErrLimit:
					status = 413
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(out)
		})
	}
	return m
}
