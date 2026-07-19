package drift

import (
	"testing"

	"github.com/thebolaji/infraward/internal/baseline"
	iwconfig "github.com/thebolaji/infraward/internal/config"
	"github.com/thebolaji/infraward/internal/tfstate"
)

func TestDiff_matchedByID(t *testing.T) {
	state := []tfstate.Resource{{Type: "aws_instance", ID: "i-123"}}
	live := []Resource{{TerraformType: "aws_instance", ID: "i-123"}}
	scanned := map[string]bool{"AWS::EC2::Instance": true}

	result := diff(state, live, scanned)
	if len(result.Unmanaged) != 0 {
		t.Errorf("expected no unmanaged, got %+v", result.Unmanaged)
	}
	if len(result.Missing) != 0 {
		t.Errorf("expected no missing, got %+v", result.Missing)
	}
	if result.Coverage.Managed != 1 {
		t.Errorf("Coverage.Managed = %d, want 1", result.Coverage.Managed)
	}
}

func TestDiff_matchedByARN(t *testing.T) {
	// aws_iam_policy's "id" attribute IS its ARN, but other types (e.g.
	// aws_lb) store id == arn too while some state configurations record
	// only the arn attribute distinctly from id -- either must match.
	state := []tfstate.Resource{{Type: "aws_iam_policy", ID: "", ARN: "arn:aws:iam::aws:policy/AdministratorAccess"}}
	live := []Resource{{TerraformType: "aws_iam_policy", ID: "arn:aws:iam::aws:policy/AdministratorAccess"}}

	result := diff(state, live, nil)
	if len(result.Unmanaged) != 0 {
		t.Errorf("expected the ARN match to mark this managed, got unmanaged=%+v", result.Unmanaged)
	}
}

func TestDiff_unmanagedWhenNoStateMatch(t *testing.T) {
	live := []Resource{{TerraformType: "aws_instance", ID: "i-999", Region: "us-east-1"}}

	result := diff(nil, live, nil)
	if len(result.Unmanaged) != 1 || result.Unmanaged[0].ID != "i-999" {
		t.Errorf("expected i-999 to be unmanaged, got %+v", result.Unmanaged)
	}
}

func TestDiff_missingWhenStateHasNoLiveMatch(t *testing.T) {
	state := []tfstate.Resource{{Type: "aws_instance", ID: "i-gone"}}
	scanned := map[string]bool{"AWS::EC2::Instance": true}

	result := diff(state, nil, scanned)
	if len(result.Missing) != 1 || result.Missing[0].ID != "i-gone" {
		t.Errorf("expected i-gone to be missing, got %+v", result.Missing)
	}
}

func TestDiff_missingSkipped_typeOutsideMappingTable(t *testing.T) {
	// A Terraform type InfraWard has no mapping for at all: it can't
	// confirm the resource is actually gone, so it must not be reported.
	state := []tfstate.Resource{{Type: "aws_totally_unmapped_type", ID: "whatever"}}

	result := diff(state, nil, nil)
	if len(result.Missing) != 0 {
		t.Errorf("expected an unmapped type to never be reported missing, got %+v", result.Missing)
	}
}

func TestDiff_missingSkipped_typeNotScannedThisRun(t *testing.T) {
	// Mapped, but excluded by --filter (or just not in scannedCCTypes for
	// this run): same rule as above -- no data means no verdict.
	state := []tfstate.Resource{{Type: "aws_instance", ID: "i-123"}}
	scanned := map[string]bool{} // AWS::EC2::Instance not scanned

	result := diff(state, nil, scanned)
	if len(result.Missing) != 0 {
		t.Errorf("expected a not-scanned type to never be reported missing, got %+v", result.Missing)
	}
}

func TestDiff_compositeIdentifierMatch(t *testing.T) {
	// Some Cloud Control identifiers are "parent|child" composites; a
	// component match against state's id/arn must still count.
	state := []tfstate.Resource{{Type: "aws_iam_role_policy", ID: "MyRole:MyPolicy"}}
	live := []Resource{{TerraformType: "aws_iam_role_policy", ID: "MyRole|MyRole:MyPolicy"}}

	result := diff(state, live, nil)
	if len(result.Unmanaged) != 0 {
		t.Errorf("expected composite identifier component match to mark this managed, got unmanaged=%+v", result.Unmanaged)
	}
}

func TestDiff_coverageCounts(t *testing.T) {
	state := []tfstate.Resource{
		{Type: "aws_instance", ID: "i-managed"},
		{Type: "aws_instance", ID: "i-gone"},
	}
	live := []Resource{
		{TerraformType: "aws_instance", ID: "i-managed"},
		{TerraformType: "aws_instance", ID: "i-unmanaged"},
	}
	scanned := map[string]bool{"AWS::EC2::Instance": true}

	result := diff(state, live, scanned)
	c := result.Coverage
	if c.TotalInAWS != 2 {
		t.Errorf("TotalInAWS = %d, want 2", c.TotalInAWS)
	}
	if c.Managed != 1 {
		t.Errorf("Managed = %d, want 1", c.Managed)
	}
	if c.Unmanaged != 1 {
		t.Errorf("Unmanaged = %d, want 1", c.Unmanaged)
	}
	if c.Missing != 1 {
		t.Errorf("Missing = %d, want 1", c.Missing)
	}
}

func TestApplySuppressions_marksButDoesNotRemove(t *testing.T) {
	result := &Result{
		Unmanaged: []Resource{
			{TerraformType: "aws_iam_policy", ID: "arn:x"},
			{TerraformType: "aws_instance", ID: "i-123"},
		},
	}
	cfg := &iwconfig.Config{Suppress: []iwconfig.Rule{{Type: "aws_iam_policy"}}}

	applySuppressions(result, cfg)

	if len(result.Unmanaged) != 2 {
		t.Fatalf("applySuppressions must not remove entries, got %d", len(result.Unmanaged))
	}
	if !result.Unmanaged[0].Ignored {
		t.Error("expected the matching resource to be marked Ignored")
	}
	if result.Unmanaged[0].IgnoredReason == "" {
		t.Error("expected a non-empty IgnoredReason")
	}
	if result.Unmanaged[1].Ignored {
		t.Error("expected the non-matching resource to stay un-ignored")
	}
}

func TestSinceLast_onlyReportsNewFindings(t *testing.T) {
	full := &Result{
		Unmanaged: []Resource{
			{TerraformType: "aws_instance", ID: "i-old"}, // was already unmanaged last scan
			{TerraformType: "aws_instance", ID: "i-new"}, // appeared since last scan
		},
		Coverage: Coverage{TotalInAWS: 10, Managed: 8, Unmanaged: 2, Missing: 0},
	}
	prevUnmanaged := map[string]bool{baseline.Key("aws_instance", "i-old"): true}
	prevMissing := map[string]bool{}

	result := sinceLast(full, prevUnmanaged, prevMissing)

	if len(result.Unmanaged) != 1 || result.Unmanaged[0].ID != "i-new" {
		t.Errorf("expected only i-new, got %+v", result.Unmanaged)
	}
	// Total inventory context is preserved even though the finding list is filtered.
	if result.Coverage.TotalInAWS != 10 || result.Coverage.Managed != 8 {
		t.Errorf("expected TotalInAWS/Managed to carry over from the full scan, got %+v", result.Coverage)
	}
	if result.Coverage.Unmanaged != 1 {
		t.Errorf("expected Coverage.Unmanaged to match the filtered list length (1), got %d", result.Coverage.Unmanaged)
	}
}

func TestFilterTypes_noFiltersReturnsEverything(t *testing.T) {
	types := []string{"AWS::EC2::Instance", "AWS::S3::Bucket"}
	got := filterTypes(types, nil)
	if len(got) != 2 {
		t.Errorf("got %v, want all types unfiltered", got)
	}
}

func TestFilterTypes_globMatchesPrefix(t *testing.T) {
	types := []string{"AWS::EC2::Instance", "AWS::EC2::VPC", "AWS::S3::Bucket"}
	got := filterTypes(types, []string{"AWS::EC2::*"})
	if len(got) != 2 {
		t.Errorf("got %v, want the 2 EC2 types only", got)
	}
	for _, ty := range got {
		if ty == "AWS::S3::Bucket" {
			t.Error("S3 type should have been filtered out")
		}
	}
}

func TestToFindings_convertsBothBuckets(t *testing.T) {
	result := &Result{
		Unmanaged: []Resource{{TerraformType: "aws_instance", ID: "i-1", Region: "us-east-1"}},
		Missing:   []Resource{{TerraformType: "aws_s3_bucket", ID: "b-1", Region: "us-east-1"}},
	}
	findings := toFindings(result)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	if findings[0].Status != "unmanaged" || findings[1].Status != "missing" {
		t.Errorf("unexpected statuses: %+v", findings)
	}
}
