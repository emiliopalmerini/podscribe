package main

import (
	"context"
	"os"

	"github.com/emiliopalmerini/podscribe/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		os.Exit(1)
	}
}
