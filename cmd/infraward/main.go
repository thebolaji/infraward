// Command infraward reconciles Terraform state against live AWS resources.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/thebolaji/infraward/internal/cli"
)

// version, commit, and date are set via -ldflags by GoReleaser at release
// build time (see .goreleaser.yml); they stay at these defaults for
// `go build`/`go install` from source.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	err := cli.NewRootCmd(fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)).Execute()
	if err == nil {
		return 0
	}

	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.Err != nil {
			fmt.Fprintln(os.Stderr, exitErr.Err)
		}
		return exitErr.Code
	}

	// Unrecognized error shape (e.g. cobra's own flag-parsing errors): treat as an error exit.
	fmt.Fprintln(os.Stderr, err)
	return 2
}
