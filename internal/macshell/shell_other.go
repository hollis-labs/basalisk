//go:build !(darwin && desktop)

package macshell

// Default is the native shell of this process. It is nil in a build without the desktop
// shell (any non-macOS build, and macOS builds without the "desktop" tag).
func Default() Controller { return nil }
