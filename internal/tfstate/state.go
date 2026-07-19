// Package tfstate parses Terraform state (local files and S3, read-only)
// into the resource records the drift engine diffs against live AWS.
package tfstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Resource is a single managed resource as recorded in Terraform state.
type Resource struct {
	Address string // e.g. "aws_s3_bucket.logs"
	Type    string // Terraform resource type, e.g. "aws_s3_bucket"
	ID      string // the "id" attribute, provider-assigned
	ARN     string // the "arn" attribute, when the resource has one
}

// State is one parsed Terraform state file.
type State struct {
	Source    string // the local path or s3:// URI this was loaded from
	Serial    int64
	Resources []Resource
}

// Client reads Terraform state from local disk and S3.
type Client struct {
	s3 *s3.Client
}

// NewClient builds a state-reading Client using the given AWS config for S3 access.
func NewClient(cfg aws.Config) *Client {
	return &Client{s3: s3.NewFromConfig(cfg)}
}

// Discover expands the raw --state values (local paths, globs, and
// s3://bucket/prefix URIs) into concrete state file sources. A prefix scan
// discovers every .tfstate key under it.
func (c *Client) Discover(ctx context.Context, patterns []string) ([]string, error) {
	var sources []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "s3://") {
			keys, err := c.discoverS3(ctx, p)
			if err != nil {
				return nil, err
			}
			sources = append(sources, keys...)
			continue
		}

		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("tfstate: invalid glob %q: %w", p, err)
		}
		if matches == nil {
			// Not a glob pattern, or a glob with zero matches: treat as a
			// literal path so a plain "foo.tfstate" still resolves.
			if _, err := os.Stat(p); err == nil {
				matches = []string{p}
			}
		}
		sources = append(sources, matches...)
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("tfstate: no state files matched %v", patterns)
	}
	return sources, nil
}

func (c *Client) discoverS3(ctx context.Context, uri string) ([]string, error) {
	if strings.HasSuffix(uri, ".tfstate") {
		return []string{uri}, nil
	}

	bucket, prefix, err := parseS3URI(uri)
	if err != nil {
		return nil, err
	}

	var keys []string
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix)}
	for {
		out, err := c.s3.ListObjectsV2(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("tfstate: list s3://%s/%s: %w", bucket, prefix, err)
		}
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			if strings.HasSuffix(key, ".tfstate") {
				keys = append(keys, fmt.Sprintf("s3://%s/%s", bucket, key))
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		in.ContinuationToken = out.NextContinuationToken
	}
	return keys, nil
}

// Load parses a single Terraform state file (local path or s3:// URI) into a State.
func (c *Client) Load(ctx context.Context, source string) (*State, error) {
	var data []byte
	var err error
	if strings.HasPrefix(source, "s3://") {
		data, err = c.readS3(ctx, source)
	} else {
		data, err = os.ReadFile(source)
	}
	if err != nil {
		return nil, fmt.Errorf("tfstate: read %s: %w", source, err)
	}

	var raw rawState
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("tfstate: parse %s: %w", source, err)
	}

	state := &State{Source: source, Serial: raw.Serial}
	for _, res := range raw.Resources {
		if res.Mode != "managed" {
			continue
		}
		for _, inst := range res.Instances {
			state.Resources = append(state.Resources, Resource{
				Address: resourceAddress(res, inst),
				Type:    res.Type,
				ID:      stringAttr(inst.Attributes, "id"),
				ARN:     stringAttr(inst.Attributes, "arn"),
			})
		}
	}
	return state, nil
}

func (c *Client) readS3(ctx context.Context, uri string) ([]byte, error) {
	bucket, key, err := parseS3URI(uri)
	if err != nil {
		return nil, err
	}
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func parseS3URI(uri string) (bucket, key string, err error) {
	rest := strings.TrimPrefix(uri, "s3://")
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", "", fmt.Errorf("tfstate: invalid s3 URI %q", uri)
	}
	bucket = parts[0]
	if len(parts) == 2 {
		key = parts[1]
	}
	return bucket, key, nil
}

// rawState mirrors the parts of the Terraform state v4 JSON format InfraWard needs.
type rawState struct {
	Serial    int64         `json:"serial"`
	Resources []rawResource `json:"resources"`
}

type rawResource struct {
	Module    string        `json:"module"`
	Mode      string        `json:"mode"`
	Type      string        `json:"type"`
	Name      string        `json:"name"`
	Instances []rawInstance `json:"instances"`
}

type rawInstance struct {
	IndexKey   any            `json:"index_key"`
	Attributes map[string]any `json:"attributes"`
}

func resourceAddress(res rawResource, inst rawInstance) string {
	addr := res.Type + "." + res.Name
	if res.Module != "" {
		addr = res.Module + "." + addr
	}
	switch k := inst.IndexKey.(type) {
	case string:
		addr += fmt.Sprintf("[%q]", k)
	case float64:
		addr += fmt.Sprintf("[%d]", int(k))
	}
	return addr
}

func stringAttr(attrs map[string]any, key string) string {
	v, _ := attrs[key].(string)
	return v
}
