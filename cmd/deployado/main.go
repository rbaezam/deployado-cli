package main

import (
	"fmt"
	"os"

	"github.com/deployado/cli/internal/cmd"
)

// Version information (set by goreleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersion(version, commit, date)

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
