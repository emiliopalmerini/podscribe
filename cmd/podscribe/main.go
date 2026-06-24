package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/emiliopalmerini/podscribe/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		os.Exit(1)
	}
}
