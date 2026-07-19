package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/thebolaji/infraward/internal/drift"
)

// renderResult writes a scan result in the requested format. "table" is the
// human-readable default; "json" is for machine consumption; "github" is a
// step-summary-friendly form for Actions. Suppressed findings are collapsed
// out of the displayed list unless showIgnored (Coverage counts are
// unaffected either way: they always reflect everything found).
func renderResult(w io.Writer, format string, result *drift.Result, showIgnored bool) error {
	display := result
	if !showIgnored {
		display = withoutIgnored(result)
	}

	switch format {
	case "json":
		return renderJSON(w, display)
	case "github":
		return renderGitHub(w, display)
	default:
		return renderTable(w, display)
	}
}

func withoutIgnored(result *drift.Result) *drift.Result {
	return &drift.Result{
		Unmanaged: filterIgnored(result.Unmanaged),
		Missing:   filterIgnored(result.Missing),
		Coverage:  result.Coverage,
	}
}

func filterIgnored(resources []drift.Resource) []drift.Resource {
	var out []drift.Resource
	for _, r := range resources {
		if !r.Ignored {
			out = append(out, r)
		}
	}
	return out
}

func renderTable(w io.Writer, result *drift.Result) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)

	fmt.Fprintln(tw, "TYPE\tID\tREGION\tSTATUS")
	for _, r := range result.Unmanaged {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.TerraformType, r.ID, r.Region, status("unmanaged", r))
	}
	for _, r := range result.Missing {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.TerraformType, r.ID, r.Region, status("missing", r))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	c := result.Coverage
	pct := 0
	if c.TotalInAWS > 0 {
		pct = c.Managed * 100 / c.TotalInAWS
	}
	_, err := fmt.Fprintf(w, "\n%d resources in AWS, %d managed (%d%%), %d unmanaged, %d missing.\n",
		c.TotalInAWS, c.Managed, pct, c.Unmanaged, c.Missing)
	return err
}

func status(base string, r drift.Resource) string {
	if r.Ignored {
		return fmt.Sprintf("%s (ignored: %s)", base, r.IgnoredReason)
	}
	return base
}

func renderJSON(w io.Writer, result *drift.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func renderGitHub(w io.Writer, result *drift.Result) error {
	c := result.Coverage
	_, err := fmt.Fprintf(w, "### InfraWard drift\n\n%d resources in AWS, %d managed, %d unmanaged, %d missing.\n",
		c.TotalInAWS, c.Managed, c.Unmanaged, c.Missing)
	return err
}
