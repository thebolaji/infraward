package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thebolaji/infraward/internal/drift"
)

// driftFlags mirrors the `infraward drift` flags described in the drift command spec.
type driftFlags struct {
	state       []string
	region      []string
	filter      []string
	output      string
	showIgnored bool
	sinceLast   bool
}

func newDriftCmd() *cobra.Command {
	var f driftFlags

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Find AWS resources that Terraform state doesn't manage",
		Long: `Scan one AWS account+region set, compare it against one or more Terraform
state files, and report unmanaged resources (in AWS, in no state), missing
resources (in state, gone from AWS), and a one-line coverage summary.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDrift(cmd, f)
		},
	}

	flags := cmd.Flags()
	flags.StringArrayVar(&f.state, "state", nil,
		"Terraform state source: local path, glob, or s3://bucket/prefix (repeatable)")
	flags.StringArrayVar(&f.region, "region", nil,
		"AWS region to scan (repeatable; defaults to the AWS config default region)")
	flags.StringArrayVar(&f.filter, "filter", nil,
		"Limit scanned Cloud Control types, e.g. 'AWS::EC2::*' (repeatable)")
	flags.StringVar(&f.output, "output", "table",
		"Output format: table, json, or github")
	flags.BoolVar(&f.showIgnored, "show-ignored", false,
		"Include suppressed findings instead of collapsing them")
	flags.BoolVar(&f.sinceLast, "since-last", false,
		"Report only what changed since the previous scan")

	cmd.MarkFlagRequired("state")

	return cmd
}

func runDrift(cmd *cobra.Command, f driftFlags) error {
	switch f.output {
	case "table", "json", "github":
	default:
		return &ExitError{Code: 2, Err: fmt.Errorf("drift: unknown --output %q (want table, json, or github)", f.output)}
	}

	opts := drift.Options{
		StateSources: f.state,
		Regions:      f.region,
		Filters:      f.filter,
		Output:       f.output,
		ShowIgnored:  f.showIgnored,
		SinceLast:    f.sinceLast,
	}

	result, err := drift.Run(cmd.Context(), opts)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}

	if err := renderResult(cmd.OutOrStdout(), f.output, result, f.showIgnored); err != nil {
		return &ExitError{Code: 2, Err: err}
	}

	if result.HasFindings() {
		return &ExitError{Code: 1}
	}
	return nil
}
