// Command basalisk is the Basalisk dashboard server, built on Substrate. The shell (config,
// logging, health, HTTP, auth, identity, secrets, database, CLI skeleton) comes from the
// manifest; this file and internal/ are the app: its data, its routes and its companion API.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/basalisk/internal/persistence"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "basalisk:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	config, err := prepare(getenv)
	if err != nil {
		return err
	}
	fsys, root := persistence.Migrations()
	database.UseMigrationFS(fsys, root)

	started, err := boot.Start(ctx, substrateApp, substrateModules, config)
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()

	b := &basalisk{app: started.App}
	rootCmd := cli.Root(started.App)
	serve := b.serveCmd(ctx)
	// `basalisk` with no command serves, like `basalisk serve`.
	rootCmd.RunE = func(c *cobra.Command, args []string) error { return serve.RunE(c, args) }
	rootCmd.Args = cobra.NoArgs
	rootCmd.AddCommand(serve)
	return rootCmd.ExecuteContext(ctx)
}
