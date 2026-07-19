package drift

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"golang.org/x/sync/errgroup"

	"github.com/thebolaji/infraward/internal/awscc"
	"github.com/thebolaji/infraward/internal/baseline"
	iwconfig "github.com/thebolaji/infraward/internal/config"
	"github.com/thebolaji/infraward/internal/mapping"
	"github.com/thebolaji/infraward/internal/route53"
	"github.com/thebolaji/infraward/internal/tfstate"
)

// maxConcurrentListings bounds parallel Cloud Control ListResources calls:
// a sensible default worker pool, with backoff on throttling. Backoff
// itself comes from the AWS SDK's standard retryer (see
// config.WithRetryMaxAttempts below).
const maxConcurrentListings = 8

// Run executes a single drift scan: discover the supplied Terraform state,
// discover live AWS resources in the target regions, diff the two, apply
// .infraward.yml suppressions, persist the scan to the local SQLite
// baseline, and return the result (filtered to only what's new since the
// previous scan, if opts.SinceLast).
//
// Suppressed findings stay in the returned Result (marked Ignored) rather
// than being dropped, so Coverage counts and the baseline both stay
// complete; it's the CLI's renderer that collapses them unless
// opts.ShowIgnored.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if err := validate(opts); err != nil {
		return nil, err
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(5))
	if err != nil {
		return nil, fmt.Errorf("drift: load AWS config: %w", err)
	}

	regions := opts.Regions
	if len(regions) == 0 {
		if cfg.Region == "" {
			return nil, fmt.Errorf("drift: no region configured; pass --region or set a default in your AWS config")
		}
		regions = []string{cfg.Region}
	}

	stateResources, err := loadState(ctx, tfstate.NewClient(cfg), opts.StateSources)
	if err != nil {
		return nil, err
	}

	suppressions, err := iwconfig.Load(iwconfig.DefaultPath)
	if err != nil {
		return nil, err
	}

	ccTypes := filterTypes(mapping.CloudControlTypes(), opts.Filters)
	ccClient := awscc.NewClient(cfg)

	liveResources, scannedCCTypes, err := discoverLive(ctx, ccClient, route53.NewClient(cfg), regions, ccTypes)
	if err != nil {
		return nil, err
	}

	full := diff(stateResources, liveResources, scannedCCTypes)

	// Tags are only ever needed to evaluate tag suppress rules, only for
	// resources that are actually unmanaged (Missing ones no longer exist in
	// AWS to have tags), and only for resources a tag rule's Type/ID/IDPattern
	// already narrows to (config.Load rejects unscoped tag rules precisely so
	// this can't degrade to "fetch tags for everything").
	if suppressions.HasTagRules() {
		if err := fetchTags(ctx, ccClient, full.Unmanaged, suppressions); err != nil {
			return nil, err
		}
	}

	applySuppressions(full, suppressions)

	store, err := baseline.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	result := full
	if opts.SinceLast {
		prevUnmanaged, prevMissing, err := store.LastFindings(ctx)
		if err != nil {
			return nil, err
		}
		result = sinceLast(full, prevUnmanaged, prevMissing)
	}

	if err := store.Record(ctx, toFindings(full)); err != nil {
		return nil, err
	}

	return result, nil
}

func validate(opts Options) error {
	if len(opts.StateSources) == 0 {
		return fmt.Errorf("drift: at least one --state source is required")
	}
	return nil
}

func loadState(ctx context.Context, client *tfstate.Client, patterns []string) ([]tfstate.Resource, error) {
	sources, err := client.Discover(ctx, patterns)
	if err != nil {
		return nil, err
	}

	var resources []tfstate.Resource
	for _, src := range sources {
		state, err := client.Load(ctx, src)
		if err != nil {
			return nil, err
		}
		resources = append(resources, state.Resources...)
	}
	return resources, nil
}

// filterTypes applies --filter globs (e.g. "AWS::EC2::*") to the Cloud
// Control types InfraWard knows about. No filters means scan everything.
func filterTypes(types, filters []string) []string {
	if len(filters) == 0 {
		return types
	}
	var out []string
	for _, t := range types {
		for _, f := range filters {
			if ok, _ := path.Match(f, t); ok {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

type discoveryJob struct {
	region, cloudControlType string
}

// discoverLive lists every live resource InfraWard knows how to find:
// Cloud Control types across all target regions (concurrently, bounded by
// maxConcurrentListings), plus the Route53 RecordSet hand-written exception
// (global, listed once). It returns the normalized resources plus the set
// of Cloud Control types actually scanned, so diff() can tell "confirmed
// absent" apart from "never looked."
func discoverLive(ctx context.Context, ccClient *awscc.Client, r53Client *route53.Client, regions, ccTypes []string) ([]Resource, map[string]bool, error) {
	var jobs []discoveryJob
	for _, region := range regions {
		for _, t := range ccTypes {
			jobs = append(jobs, discoveryJob{region: region, cloudControlType: t})
		}
	}

	ccResults := make([][]awscc.Resource, len(jobs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentListings)

	for i, j := range jobs {
		i, j := i, j
		g.Go(func() error {
			res, err := ccClient.ListResources(gctx, j.region, j.cloudControlType)
			if err != nil {
				return err
			}
			ccResults[i] = res
			return nil
		})
	}

	var recordSets []route53.RecordSet
	g.Go(func() error {
		rs, err := r53Client.ListRecordSets(gctx)
		if err != nil {
			return err
		}
		recordSets = rs
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	scanned := make(map[string]bool, len(ccTypes))
	for _, t := range ccTypes {
		scanned[t] = true
	}

	var live []Resource
	for _, res := range ccResults {
		for _, r := range res {
			m, ok := mapping.ByCloudControlType(r.CloudControlType)
			if !ok {
				continue
			}
			live = append(live, Resource{
				TerraformType:    m.TerraformType,
				CloudControlType: r.CloudControlType,
				ID:               r.Identifier,
				Region:           r.Region,
			})
		}
	}
	for _, rs := range recordSets {
		live = append(live, Resource{
			TerraformType: "aws_route53_record",
			ID:            fmt.Sprintf("%s_%s_%s", rs.HostedZoneID, rs.Name, rs.Type),
		})
	}

	return live, scanned, nil
}

// fetchTags populates Tags on each resource that a tag rule could actually
// match (cfg.NeedsTagCheck), via Cloud Control's GetResource, concurrently
// and bounded the same way discoverLive's listing is. Resources with no
// CloudControlType (the Route53 RecordSet hand-written exception) are
// skipped: Route53 record sets don't support tags at all.
func fetchTags(ctx context.Context, ccClient *awscc.Client, resources []Resource, cfg *iwconfig.Config) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentListings)

	for i := range resources {
		if resources[i].CloudControlType == "" {
			continue
		}
		if !cfg.NeedsTagCheck(resources[i].TerraformType, resources[i].ID) {
			continue
		}
		i := i
		g.Go(func() error {
			tags, err := ccClient.GetResourceTags(gctx, resources[i].Region, resources[i].CloudControlType, resources[i].ID)
			if err != nil {
				return err
			}
			resources[i].Tags = tags
			return nil
		})
	}

	return g.Wait()
}

type stateKey struct {
	terraformType string
	id            string
}

// diff matches live resources against state by Terraform type and
// identifier (state's "id" or "arn" attribute), producing Unmanaged
// (live, unmatched), Missing (state, unmatched, but only for types this
// scan actually covered), and the Coverage summary.
func diff(stateResources []tfstate.Resource, live []Resource, scannedCCTypes map[string]bool) *Result {
	byKey := make(map[stateKey]int, len(stateResources)*2)
	matched := make([]bool, len(stateResources))
	for i, s := range stateResources {
		if s.ID != "" {
			byKey[stateKey{s.Type, s.ID}] = i
		}
		if s.ARN != "" {
			byKey[stateKey{s.Type, s.ARN}] = i
		}
	}

	var unmanaged []Resource
	for _, l := range live {
		if idx, ok := lookupState(byKey, l.TerraformType, l.ID); ok {
			matched[idx] = true
			continue
		}
		unmanaged = append(unmanaged, l)
	}

	var missing []Resource
	for i, s := range stateResources {
		if matched[i] {
			continue
		}
		m, ok := mapping.ByTerraformType(s.Type)
		if !ok {
			continue // outside v0.1's ~30-type coverage: neither confirmed present nor absent
		}
		if m.Source == mapping.CloudControl && !scannedCCTypes[m.CloudControlType] {
			continue // filtered out or not scanned this run
		}
		missing = append(missing, Resource{TerraformType: s.Type, ID: s.ID})
	}

	return &Result{
		Unmanaged: unmanaged,
		Missing:   missing,
		Coverage: Coverage{
			TotalInAWS: len(live),
			Managed:    len(live) - len(unmanaged),
			Unmanaged:  len(unmanaged),
			Missing:    len(missing),
		},
	}
}

// applySuppressions marks Unmanaged/Missing resources matched by an
// .infraward.yml rule as Ignored, in place. It does not remove them: that
// keeps Coverage counts and the SQLite baseline honest (suppressed items
// are counted but collapsed in output) and leaves the collapsing to the
// CLI's renderer.
func applySuppressions(result *Result, cfg *iwconfig.Config) {
	mark := func(resources []Resource) {
		for i := range resources {
			if ok, reason := cfg.Suppressed(resources[i].TerraformType, resources[i].ID, resources[i].Tags); ok {
				resources[i].Ignored = true
				resources[i].IgnoredReason = reason
			}
		}
	}
	mark(result.Unmanaged)
	mark(result.Missing)
}

// lookupState matches a live identifier against state's id/arn attributes.
// Some Cloud Control identifiers are composite (parts joined with "|"), e.g.
// nested resources identified by parent+child; a component match counts too.
func lookupState(byKey map[stateKey]int, terraformType, liveID string) (int, bool) {
	if idx, ok := byKey[stateKey{terraformType, liveID}]; ok {
		return idx, true
	}
	if strings.Contains(liveID, "|") {
		for _, part := range strings.Split(liveID, "|") {
			if idx, ok := byKey[stateKey{terraformType, part}]; ok {
				return idx, true
			}
		}
	}
	return 0, false
}

// sinceLast filters a full result down to only findings not present in the
// previous scan's baseline ("what appeared since the last scan").
func sinceLast(full *Result, prevUnmanaged, prevMissing map[string]bool) *Result {
	newUnmanaged := filterNew(full.Unmanaged, prevUnmanaged)
	newMissing := filterNew(full.Missing, prevMissing)
	return &Result{
		Unmanaged: newUnmanaged,
		Missing:   newMissing,
		Coverage: Coverage{
			TotalInAWS: full.Coverage.TotalInAWS,
			Managed:    full.Coverage.Managed,
			Unmanaged:  len(newUnmanaged),
			Missing:    len(newMissing),
		},
	}
}

func filterNew(resources []Resource, prev map[string]bool) []Resource {
	var out []Resource
	for _, r := range resources {
		if !prev[baseline.Key(r.TerraformType, r.ID)] {
			out = append(out, r)
		}
	}
	return out
}

func toFindings(result *Result) []baseline.Finding {
	findings := make([]baseline.Finding, 0, len(result.Unmanaged)+len(result.Missing))
	for _, r := range result.Unmanaged {
		findings = append(findings, baseline.Finding{Status: "unmanaged", TerraformType: r.TerraformType, ID: r.ID, Region: r.Region})
	}
	for _, r := range result.Missing {
		findings = append(findings, baseline.Finding{Status: "missing", TerraformType: r.TerraformType, ID: r.ID, Region: r.Region})
	}
	return findings
}
