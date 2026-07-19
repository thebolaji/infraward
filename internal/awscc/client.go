// Package awscc discovers live AWS resources through the AWS Cloud Control
// API (ListResources), which is what lets InfraWard cover ~30 resource
// types from one data-driven mapping table instead of a hand-written reader
// per type.
package awscc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// Resource is one AWS resource discovered via Cloud Control, identified but
// not yet fetched in full: v0.1.0's unmanaged/missing detection only needs
// the identifier. Tags (for suppression rules) are fetched separately via
// GetResourceTags, and only when actually needed -- see that function.
type Resource struct {
	CloudControlType string
	Identifier       string
	Region           string
}

// Client discovers live AWS resources through Cloud Control.
type Client struct {
	cfg aws.Config
}

// NewClient builds a Cloud Control Client from the given AWS config.
func NewClient(cfg aws.Config) *Client {
	return &Client{cfg: cfg}
}

// ListResources lists every resource of the given Cloud Control type
// (e.g. "AWS::S3::Bucket") in a region.
func (c *Client) ListResources(ctx context.Context, region, cloudControlType string) ([]Resource, error) {
	cli := cloudcontrol.NewFromConfig(c.cfg, func(o *cloudcontrol.Options) {
		o.Region = region
	})

	var resources []Resource
	in := &cloudcontrol.ListResourcesInput{TypeName: aws.String(cloudControlType)}
	for {
		out, err := cli.ListResources(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("awscc: ListResources %s in %s: %w", cloudControlType, region, err)
		}
		for _, d := range out.ResourceDescriptions {
			resources = append(resources, Resource{
				CloudControlType: cloudControlType,
				Identifier:       aws.ToString(d.Identifier),
				Region:           region,
			})
		}
		if out.NextToken == nil {
			break
		}
		in.NextToken = out.NextToken
	}
	return resources, nil
}

// GetResourceTags fetches one resource's tags via GetResource. This is a
// separate, more expensive call from ListResources (one request per
// resource, not per type), so callers should only use it for resources
// that actually need tag data -- e.g. only for findings a suppression rule
// might match, not for every resource in the account.
//
// Cloud Control's Properties JSON conventionally represents tags as a
// "Tags" property shaped like CloudFormation's Tag type
// ([{"Key":.., "Value":..}, ...]), but this isn't guaranteed uniform across
// every resource type's schema: a type without that shape just yields no
// tags, not an error.
func (c *Client) GetResourceTags(ctx context.Context, region, cloudControlType, identifier string) (map[string]string, error) {
	cli := cloudcontrol.NewFromConfig(c.cfg, func(o *cloudcontrol.Options) {
		o.Region = region
	})

	out, err := cli.GetResource(ctx, &cloudcontrol.GetResourceInput{
		TypeName:   aws.String(cloudControlType),
		Identifier: aws.String(identifier),
	})
	if err != nil {
		return nil, fmt.Errorf("awscc: GetResource %s %s in %s: %w", cloudControlType, identifier, region, err)
	}
	if out.ResourceDescription == nil || out.ResourceDescription.Properties == nil {
		return nil, nil
	}

	var props map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(out.ResourceDescription.Properties)), &props); err != nil {
		return nil, fmt.Errorf("awscc: parse properties for %s %s: %w", cloudControlType, identifier, err)
	}
	return extractTags(props), nil
}

func extractTags(props map[string]any) map[string]string {
	list, ok := props["Tags"].([]any)
	if !ok {
		return nil
	}

	tags := make(map[string]string, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := m["Key"].(string)
		value, _ := m["Value"].(string)
		if key != "" {
			tags[key] = value
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}
