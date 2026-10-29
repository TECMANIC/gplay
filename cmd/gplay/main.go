package main

import (
	"context"
	"os"

	"github.com/TECMANIC/gplay/internal/cli"
)

// Set by GoReleaser via -X. Defaults keep `go run` and `go test` usable.
var (
	version = "v0.1.0-dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.SetBuildInfo(version, commit, date)
	os.Exit(cli.Execute(context.Background(), os.Args[1:], cli.IO{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}))
}
