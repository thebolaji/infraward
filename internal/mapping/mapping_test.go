package mapping

import "testing"

// TestTableIntegrity guards the property this whole package exists for:
// Table is data anyone can extend, so it needs to stay internally
// consistent without a human re-checking every entry by eye.
func TestTableIntegrity(t *testing.T) {
	seenTerraform := map[string]bool{}
	seenCloudControl := map[string]bool{}

	for _, m := range Table {
		if m.TerraformType == "" {
			t.Errorf("entry with empty TerraformType (CloudControlType=%q)", m.CloudControlType)
		}
		if seenTerraform[m.TerraformType] {
			t.Errorf("duplicate TerraformType %q", m.TerraformType)
		}
		seenTerraform[m.TerraformType] = true

		switch m.Source {
		case CloudControl:
			if m.CloudControlType == "" {
				t.Errorf("%s: CloudControl source with empty CloudControlType", m.TerraformType)
				continue
			}
			if seenCloudControl[m.CloudControlType] {
				t.Errorf("duplicate CloudControlType %q", m.CloudControlType)
			}
			seenCloudControl[m.CloudControlType] = true
		case HandWritten:
			if m.CloudControlType != "" {
				t.Errorf("%s: HandWritten source has a CloudControlType (%q); should be empty", m.TerraformType, m.CloudControlType)
			}
		default:
			t.Errorf("%s: unknown Source %v", m.TerraformType, m.Source)
		}
	}
}

func TestByTerraformType(t *testing.T) {
	m, ok := ByTerraformType("aws_s3_bucket")
	if !ok {
		t.Fatal("expected aws_s3_bucket to be found")
	}
	if m.CloudControlType != "AWS::S3::Bucket" {
		t.Errorf("aws_s3_bucket CloudControlType = %q, want AWS::S3::Bucket", m.CloudControlType)
	}

	if _, ok := ByTerraformType("aws_totally_made_up_resource"); ok {
		t.Error("expected unknown Terraform type to not be found")
	}
}

func TestByCloudControlType(t *testing.T) {
	m, ok := ByCloudControlType("AWS::EC2::Instance")
	if !ok {
		t.Fatal("expected AWS::EC2::Instance to be found")
	}
	if m.TerraformType != "aws_instance" {
		t.Errorf("AWS::EC2::Instance TerraformType = %q, want aws_instance", m.TerraformType)
	}

	if _, ok := ByCloudControlType("AWS::Totally::MadeUp"); ok {
		t.Error("expected unknown Cloud Control type to not be found")
	}

	// The Route53 RecordSet exception has no Cloud Control type at all, so
	// looking it up by (empty) CloudControlType must not accidentally match.
	if _, ok := ByCloudControlType(""); ok {
		t.Error("empty CloudControlType should never resolve to a mapping")
	}
}

func TestCloudControlTypes(t *testing.T) {
	types := CloudControlTypes()
	want := 0
	for _, m := range Table {
		if m.Source == CloudControl {
			want++
		}
	}
	if len(types) != want {
		t.Errorf("CloudControlTypes() returned %d entries, want %d", len(types), want)
	}
	for _, ct := range types {
		if ct == "" {
			t.Error("CloudControlTypes() returned an empty type name")
		}
	}
}

func TestHandWrittenTypes(t *testing.T) {
	types := HandWrittenTypes()
	found := false
	for _, tt := range types {
		if tt == "aws_route53_record" {
			found = true
		}
	}
	if !found {
		t.Error("expected aws_route53_record among HandWrittenTypes(), the one confirmed v0.1 exception")
	}
}
