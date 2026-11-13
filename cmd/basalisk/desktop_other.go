//go:build !(darwin && desktop)

package main

import (
	"context"

	"github.com/spf13/cobra"
)

// The desktop shell is macOS only and built with the "desktop" tag (scripts/build-macos-app.sh).
// Every other build has no desktop command.
func (b *basalisk) desktopCmd(context.Context) *cobra.Command { return nil }

func inAppBundle() bool { return false }

func desktopLogging() {}

func desktopConfig(map[string]any) {}

func desktopEnv() {}
