// Package mapping is the one place InfraWard owns per-type AWS knowledge:
// Terraform resource type <-> Cloud Control type name. It is data, not code,
// by design, so that adding a resource type is a table edit, not a new reader.
package mapping

// Source distinguishes resources discoverable via the AWS Cloud Control API
// (List/GetResource, driven by its schema registry) from the small set that
// need a hand-written reader because Cloud Control doesn't cover them.
type Source int

const (
	// CloudControl resources are listed/read through the Cloud Control API.
	CloudControl Source = iota
	// HandWritten resources have no Cloud Control List/Read handler and are
	// read via a native service API instead. Keep these readers in their
	// own clearly-marked package (e.g. internal/route53).
	HandWritten
)

// Mapping ties one Terraform resource type to its Cloud Control type name.
// CloudControlType is empty for HandWritten entries.
type Mapping struct {
	TerraformType    string
	CloudControlType string
	Source           Source
}

// Table is the v0.1.0 resource-type mapping: every Terraform type InfraWard
// can scan for drift. Every entry here has List+Read Cloud Control coverage
// confirmed against a real account, except the Route53 RecordSet exception
// below. Do not add entries without verifying coverage against a real
// account first -- the whole point of this table is that it stays
// trustworthy without hand-testing every type on every use.
var Table = []Mapping{
	// EC2
	{TerraformType: "aws_instance", CloudControlType: "AWS::EC2::Instance", Source: CloudControl},
	{TerraformType: "aws_vpc", CloudControlType: "AWS::EC2::VPC", Source: CloudControl},
	{TerraformType: "aws_subnet", CloudControlType: "AWS::EC2::Subnet", Source: CloudControl},
	{TerraformType: "aws_security_group", CloudControlType: "AWS::EC2::SecurityGroup", Source: CloudControl},

	// S3
	{TerraformType: "aws_s3_bucket", CloudControlType: "AWS::S3::Bucket", Source: CloudControl},

	// IAM
	{TerraformType: "aws_iam_role", CloudControlType: "AWS::IAM::Role", Source: CloudControl},
	// aws_iam_policy maps to ManagedPolicy: there is no bare AWS::IAM::Policy type.
	{TerraformType: "aws_iam_policy", CloudControlType: "AWS::IAM::ManagedPolicy", Source: CloudControl},
	// aws_iam_role_policy (AWS::IAM::RolePolicy) is NOT included: Cloud
	// Control's List handler for this type returns
	// UnsupportedActionException ("does not support LIST action"). Re-add
	// once a working discovery path (possibly GetResource-only via a known
	// identifier, not ListResources) is confirmed.

	// Lambda
	{TerraformType: "aws_lambda_function", CloudControlType: "AWS::Lambda::Function", Source: CloudControl},

	// RDS
	{TerraformType: "aws_db_instance", CloudControlType: "AWS::RDS::DBInstance", Source: CloudControl},

	// DynamoDB
	{TerraformType: "aws_dynamodb_table", CloudControlType: "AWS::DynamoDB::Table", Source: CloudControl},

	// CloudWatch
	{TerraformType: "aws_cloudwatch_metric_alarm", CloudControlType: "AWS::CloudWatch::Alarm", Source: CloudControl},
	{TerraformType: "aws_cloudwatch_composite_alarm", CloudControlType: "AWS::CloudWatch::CompositeAlarm", Source: CloudControl},
	{TerraformType: "aws_cloudwatch_dashboard", CloudControlType: "AWS::CloudWatch::Dashboard", Source: CloudControl},
	{TerraformType: "aws_cloudwatch_metric_stream", CloudControlType: "AWS::CloudWatch::MetricStream", Source: CloudControl},
	{TerraformType: "aws_cloudwatch_log_group", CloudControlType: "AWS::Logs::LogGroup", Source: CloudControl},
	// aws_cloudwatch_log_stream (AWS::Logs::LogStream) is NOT included:
	// same parent-scoping issue as RolePolicy/Listener/ListenerRule above
	// — its List handler returns InvalidRequestException requiring
	// LogGroupName. Re-add once hierarchical discovery is supported.

	// ELB (ALB/NLB, i.e. ELBv2). LoadBalancer and TargetGroup are flat
	// list-all. Listener and ListenerRule are NOT included: their List
	// handlers require a parent-scoped ResourceModel (ListenerRule needs
	// ListenerArn; Listener likely needs LoadBalancerArn), not a flat
	// ListResources call. Re-add once the engine supports hierarchical
	// (parent-scoped) discovery.
	{TerraformType: "aws_lb", CloudControlType: "AWS::ElasticLoadBalancingV2::LoadBalancer", Source: CloudControl},
	{TerraformType: "aws_lb_target_group", CloudControlType: "AWS::ElasticLoadBalancingV2::TargetGroup", Source: CloudControl},

	// Route53: HostedZone is Cloud Control covered; individual records are not
	// (the one confirmed v0.1 gap) and need the hand-written reader in
	// internal/route53.
	{TerraformType: "aws_route53_zone", CloudControlType: "AWS::Route53::HostedZone", Source: CloudControl},
	{TerraformType: "aws_route53_record", CloudControlType: "", Source: HandWritten},
}

var (
	byTerraformType    map[string]Mapping
	byCloudControlType map[string]Mapping
)

func init() {
	byTerraformType = make(map[string]Mapping, len(Table))
	byCloudControlType = make(map[string]Mapping, len(Table))
	for _, m := range Table {
		byTerraformType[m.TerraformType] = m
		if m.CloudControlType != "" {
			byCloudControlType[m.CloudControlType] = m
		}
	}
}

// ByTerraformType looks up a mapping by its Terraform resource type (e.g. "aws_s3_bucket").
func ByTerraformType(terraformType string) (Mapping, bool) {
	m, ok := byTerraformType[terraformType]
	return m, ok
}

// ByCloudControlType looks up a mapping by its Cloud Control type name (e.g. "AWS::S3::Bucket").
func ByCloudControlType(cloudControlType string) (Mapping, bool) {
	m, ok := byCloudControlType[cloudControlType]
	return m, ok
}

// CloudControlTypes returns the Cloud Control type names in Table, i.e. every
// type the drift engine can discover via ListResources rather than a
// hand-written reader.
func CloudControlTypes() []string {
	types := make([]string, 0, len(Table))
	for _, m := range Table {
		if m.Source == CloudControl {
			types = append(types, m.CloudControlType)
		}
	}
	return types
}

// HandWrittenTypes returns the Terraform resource types in Table that need a
// hand-written reader instead of Cloud Control.
func HandWrittenTypes() []string {
	var types []string
	for _, m := range Table {
		if m.Source == HandWritten {
			types = append(types, m.TerraformType)
		}
	}
	return types
}
