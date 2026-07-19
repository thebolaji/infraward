package tfstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const sampleState = `{
  "version": 4,
  "terraform_version": "1.7.0",
  "serial": 3,
  "lineage": "abc",
  "resources": [
    {
      "mode": "managed",
      "type": "aws_s3_bucket",
      "name": "logs",
      "instances": [
        { "attributes": { "id": "my-logs-bucket", "arn": "arn:aws:s3:::my-logs-bucket" } }
      ]
    },
    {
      "mode": "managed",
      "type": "aws_instance",
      "name": "web",
      "instances": [
        { "index_key": 0, "attributes": { "id": "i-0123456789abcdef0" } },
        { "index_key": 1, "attributes": { "id": "i-0fedcba9876543210" } }
      ]
    },
    {
      "mode": "managed",
      "type": "aws_iam_role_policy",
      "name": "inline",
      "module": "module.iam",
      "instances": [
        { "index_key": "admin", "attributes": { "id": "role:admin" } }
      ]
    },
    {
      "mode": "data",
      "type": "aws_ami",
      "name": "ubuntu",
      "instances": [ { "attributes": { "id": "ami-123" } } ]
    }
  ]
}`

func newTestClient() *Client {
	return NewClient(aws.Config{})
}

func TestLoad_local(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.tfstate")
	if err := os.WriteFile(path, []byte(sampleState), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := newTestClient().Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if state.Serial != 3 {
		t.Errorf("Serial = %d, want 3", state.Serial)
	}

	// Exactly 4 managed instances: 1 bucket + 2 instance for_each entries +
	// 1 module resource. The data resource must be excluded entirely.
	if len(state.Resources) != 4 {
		t.Fatalf("got %d resources, want 4: %+v", len(state.Resources), state.Resources)
	}

	want := map[string]Resource{
		"aws_s3_bucket.logs": {
			Address: "aws_s3_bucket.logs", Type: "aws_s3_bucket",
			ID: "my-logs-bucket", ARN: "arn:aws:s3:::my-logs-bucket",
		},
		"aws_instance.web[0]": {
			Address: "aws_instance.web[0]", Type: "aws_instance", ID: "i-0123456789abcdef0",
		},
		"aws_instance.web[1]": {
			Address: "aws_instance.web[1]", Type: "aws_instance", ID: "i-0fedcba9876543210",
		},
		`module.iam.aws_iam_role_policy.inline["admin"]`: {
			Address: `module.iam.aws_iam_role_policy.inline["admin"]`, Type: "aws_iam_role_policy", ID: "role:admin",
		},
	}

	for _, r := range state.Resources {
		w, ok := want[r.Address]
		if !ok {
			t.Errorf("unexpected resource address %q", r.Address)
			continue
		}
		if r != w {
			t.Errorf("resource %q = %+v, want %+v", r.Address, r, w)
		}
		delete(want, r.Address)
	}
	for addr := range want {
		t.Errorf("expected resource %q not found", addr)
	}
}

func TestLoad_missingFile(t *testing.T) {
	_, err := newTestClient().Load(context.Background(), filepath.Join(t.TempDir(), "nope.tfstate"))
	if err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestLoad_invalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.tfstate")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := newTestClient().Load(context.Background(), path)
	if err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestDiscover_glob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.tfstate", "b.tfstate", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sampleState), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sources, err := newTestClient().Discover(context.Background(), []string{filepath.Join(dir, "*.tfstate")})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("got %d sources, want 2 (the .txt file must not match): %v", len(sources), sources)
	}
}

func TestDiscover_literalPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.tfstate")
	if err := os.WriteFile(path, []byte(sampleState), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := newTestClient().Discover(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 1 || sources[0] != path {
		t.Errorf("Discover() = %v, want [%s]", sources, path)
	}
}

func TestDiscover_noMatches(t *testing.T) {
	_, err := newTestClient().Discover(context.Background(), []string{filepath.Join(t.TempDir(), "*.tfstate")})
	if err == nil {
		t.Error("expected an error when nothing matches")
	}
}

func TestParseS3URI(t *testing.T) {
	cases := []struct {
		uri        string
		wantBucket string
		wantKey    string
		wantErr    bool
	}{
		{"s3://my-bucket/prod/terraform.tfstate", "my-bucket", "prod/terraform.tfstate", false},
		{"s3://my-bucket/", "my-bucket", "", false},
		{"s3://my-bucket", "my-bucket", "", false},
		{"s3://", "", "", true},
	}
	for _, c := range cases {
		bucket, key, err := parseS3URI(c.uri)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseS3URI(%q): expected error", c.uri)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseS3URI(%q): unexpected error %v", c.uri, err)
			continue
		}
		if bucket != c.wantBucket || key != c.wantKey {
			t.Errorf("parseS3URI(%q) = (%q, %q), want (%q, %q)", c.uri, bucket, key, c.wantBucket, c.wantKey)
		}
	}
}

func TestDiscover_s3ExactKeyDoesNotList(t *testing.T) {
	// A source ending in .tfstate is treated as a literal key, not a
	// prefix -- it must not attempt to call ListObjectsV2 (which would need
	// real AWS credentials / network access in this offline test).
	sources, err := newTestClient().Discover(context.Background(), []string{"s3://my-bucket/prod/terraform.tfstate"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 1 || sources[0] != "s3://my-bucket/prod/terraform.tfstate" {
		t.Errorf("Discover() = %v", sources)
	}
}
