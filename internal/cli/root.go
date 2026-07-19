// Package cli wires InfraWard's cobra commands. It owns flag parsing and
// exit-code plumbing; the actual scanning logic lives in internal/drift.
package cli

import "github.com/spf13/cobra"

// NewRootCmd builds the infraward root command. Only `drift` is wired for
// v0.1.0; `index` and `adopt` land in later milestones.
//
// version is injected by main (via GoReleaser's ldflags in release builds,
// "dev" otherwise) and drives cobra's built-in --version flag.
func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "infraward",
		Short:   "Reconcile Terraform state against what actually exists in AWS",
		Version: version,
		Long: `InfraWard reconciles Terraform state against live AWS resources.

It answers what driftctl used to: what exists in your AWS account that no
Terraform state manages, what's in state but gone from AWS, and (in later
subcommands) how to adopt unmanaged resources and index across many states.

InfraWard is read-only: it never writes to AWS.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newDriftCmd())

	return cmd
}
