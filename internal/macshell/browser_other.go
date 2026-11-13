//go:build !(darwin && desktop)

package macshell

// DefaultBrowser is nil outside the macOS desktop build: there are no native views.
func DefaultBrowser() Browser { return nil }
