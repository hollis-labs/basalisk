package webui

import (
	"io/fs"
	"testing"
)

// The embed must compile and Assets must work even when the UI is unbuilt and
// dist/ holds only .gitkeep.
func TestAssetsOnUnbuiltTree(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(assets, "."); err != nil {
		t.Fatalf("assets root is not readable: %v", err)
	}
}
