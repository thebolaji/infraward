// Package route53 is the one hand-written reader in v0.1.0, kept isolated:
// hand-written readers are allowed only for high-value types Cloud Control
// doesn't cover well, in one clearly-marked package. AWS::Route53::RecordSet
// has no Cloud Control List/Read handler (HostedZone itself is covered; the
// records inside it are not), so this calls Route53's native
// ListResourceRecordSets API directly.
package route53

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// RecordSet is a single DNS record inside a hosted zone.
type RecordSet struct {
	HostedZoneID string
	Name         string
	Type         string // e.g. "A", "CNAME", "MX"
}

// Client discovers Route53 record sets. Route53 is a global service; it
// does not take a region.
type Client struct {
	r53 *route53.Client
}

// NewClient builds a Route53 Client from the given AWS config.
func NewClient(cfg aws.Config) *Client {
	return &Client{r53: route53.NewFromConfig(cfg)}
}

// ListRecordSets lists DNS record sets across every hosted zone in the account.
func (c *Client) ListRecordSets(ctx context.Context) ([]RecordSet, error) {
	zoneIDs, err := c.listHostedZoneIDs(ctx)
	if err != nil {
		return nil, err
	}

	var records []RecordSet
	for _, zoneID := range zoneIDs {
		rs, err := c.listRecordSetsForZone(ctx, zoneID)
		if err != nil {
			return nil, err
		}
		records = append(records, rs...)
	}
	return records, nil
}

func (c *Client) listHostedZoneIDs(ctx context.Context) ([]string, error) {
	var ids []string
	in := &route53.ListHostedZonesInput{}
	for {
		out, err := c.r53.ListHostedZones(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("route53: ListHostedZones: %w", err)
		}
		for _, z := range out.HostedZones {
			ids = append(ids, aws.ToString(z.Id))
		}
		if !out.IsTruncated {
			break
		}
		in.Marker = out.NextMarker
	}
	return ids, nil
}

func (c *Client) listRecordSetsForZone(ctx context.Context, zoneID string) ([]RecordSet, error) {
	var records []RecordSet
	in := &route53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)}
	for {
		out, err := c.r53.ListResourceRecordSets(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("route53: ListResourceRecordSets %s: %w", zoneID, err)
		}
		for _, rs := range out.ResourceRecordSets {
			records = append(records, RecordSet{
				HostedZoneID: zoneID,
				Name:         aws.ToString(rs.Name),
				Type:         string(rs.Type),
			})
		}
		if !out.IsTruncated {
			break
		}
		in.StartRecordName = out.NextRecordName
		in.StartRecordType = out.NextRecordType
		in.StartRecordIdentifier = out.NextRecordIdentifier
	}
	return records, nil
}
