package baseline

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := openAt(context.Background(), filepath.Join(t.TempDir(), "infraward.db"))
	if err != nil {
		t.Fatalf("openAt() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestLastFindings_noPreviousScan(t *testing.T) {
	store := openTestStore(t)

	unmanaged, missing, err := store.LastFindings(context.Background())
	if err != nil {
		t.Fatalf("LastFindings() error = %v", err)
	}
	if len(unmanaged) != 0 || len(missing) != 0 {
		t.Errorf("expected empty maps with no scans recorded, got unmanaged=%v missing=%v", unmanaged, missing)
	}
}

func TestRecordAndLastFindings_roundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	findings := []Finding{
		{Status: "unmanaged", TerraformType: "aws_instance", ID: "i-123", Region: "us-east-1"},
		{Status: "missing", TerraformType: "aws_s3_bucket", ID: "my-bucket", Region: "us-east-1"},
	}
	if err := store.Record(ctx, findings); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	unmanaged, missing, err := store.LastFindings(ctx)
	if err != nil {
		t.Fatalf("LastFindings() error = %v", err)
	}
	if !unmanaged[Key("aws_instance", "i-123")] {
		t.Error("expected the recorded unmanaged finding to be in LastFindings")
	}
	if !missing[Key("aws_s3_bucket", "my-bucket")] {
		t.Error("expected the recorded missing finding to be in LastFindings")
	}
	if len(unmanaged) != 1 || len(missing) != 1 {
		t.Errorf("expected exactly one entry per bucket, got unmanaged=%v missing=%v", unmanaged, missing)
	}
}

func TestLastFindings_onlyReflectsMostRecentScan(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, []Finding{{Status: "unmanaged", TerraformType: "aws_instance", ID: "old"}}); err != nil {
		t.Fatalf("first Record() error = %v", err)
	}
	if err := store.Record(ctx, []Finding{{Status: "unmanaged", TerraformType: "aws_instance", ID: "new"}}); err != nil {
		t.Fatalf("second Record() error = %v", err)
	}

	unmanaged, _, err := store.LastFindings(ctx)
	if err != nil {
		t.Fatalf("LastFindings() error = %v", err)
	}
	if unmanaged[Key("aws_instance", "old")] {
		t.Error("expected the first scan's finding to not appear once a newer scan has been recorded")
	}
	if !unmanaged[Key("aws_instance", "new")] {
		t.Error("expected the second scan's finding to be the one returned")
	}
}

func TestKey_distinguishesTypeFromID(t *testing.T) {
	// A naive concatenation like terraformType+id could collide across
	// different type/id splits; Key must not.
	a := Key("aws_i", "nstance-123")
	b := Key("aws_instance", "-123")
	if a == b {
		t.Errorf("Key collision: Key(%q,%q) == Key(%q,%q)", "aws_i", "nstance-123", "aws_instance", "-123")
	}
}
