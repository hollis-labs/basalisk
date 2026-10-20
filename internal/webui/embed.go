// Package webui supplies embedded assets to the maintained Chimera host.
package webui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/hollis-labs/chimera/host"
)

const BasePath = "/"

//go:embed all:dist
var embedded embed.FS

func New(routes http.Handler) (*host.Host, error) {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	return host.New(host.Config{Name: "basalisk", Assets: assets, BasePath: BasePath, Routes: routes})
}
