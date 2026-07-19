package drift

// Options configures a single drift scan. It is the internal counterpart of
// the `infraward drift` CLI flags.
type Options struct {
	// StateSources are the raw --state values: local paths, globs, or
	// s3://bucket/prefix URIs. Expanded into concrete state files by
	// internal/tfstate.Discover.
	StateSources []string
	// Regions are the AWS regions to scan. Empty means the AWS config's
	// default region.
	Regions []string
	// Filters limit scanned Cloud Control types, e.g. "AWS::EC2::*".
	Filters []string
	// Output selects the result renderer: "table" (default), "json", or "github".
	Output string
	// ShowIgnored includes suppressed findings in the result instead of collapsing them.
	ShowIgnored bool
	// SinceLast reports only what changed since the previous scan, per the SQLite baseline.
	SinceLast bool
}

// Resource is a single AWS resource observed during a scan, normalized
// across the Cloud Control and hand-written discovery paths.
type Resource struct {
	TerraformType    string            `json:"terraformType"`
	CloudControlType string            `json:"cloudControlType,omitempty"` // empty for hand-written types, e.g. aws_route53_record
	ID               string            `json:"id"`                         // provider-assigned ID/ARN
	Region           string            `json:"region,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	Ignored          bool              `json:"ignored,omitempty"`
	IgnoredReason    string            `json:"ignoredReason,omitempty"` // e.g. "tag managed-by=console", set when Ignored
}

// Result is the outcome of a drift scan.
type Result struct {
	// Unmanaged resources exist in AWS but are in no supplied state.
	Unmanaged []Resource `json:"unmanaged"`
	// Missing resources are in state but no longer exist in AWS.
	Missing  []Resource `json:"missing"`
	Coverage Coverage   `json:"coverage"`
}

// Coverage is the one-line scan summary, e.g.
// "214 resources in AWS, 167 managed (78%), 41 unmanaged, 6 missing."
type Coverage struct {
	TotalInAWS int `json:"totalInAws"`
	Managed    int `json:"managed"`
	Unmanaged  int `json:"unmanaged"`
	Missing    int `json:"missing"`
}

// HasFindings reports whether the result contains anything worth a non-zero
// exit code (unmanaged or missing resources).
func (r *Result) HasFindings() bool {
	if r == nil {
		return false
	}
	return len(r.Unmanaged) > 0 || len(r.Missing) > 0
}
