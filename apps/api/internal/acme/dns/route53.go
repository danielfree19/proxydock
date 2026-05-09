package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// Route53 is a DNS-01 provider backed by AWS Route53.
//
// Config JSON shape:
//
//	{
//	  "zone_id":    "Z2ABCDEFGHIJ",         // optional
//	  "zone_name":  "example.com",          // optional, used iff zone_id absent
//	  "region":     "us-east-1",            // optional, defaults to AWS SDK default
//	  "access_key": "AKIA...",              // optional
//	  "secret_key": "..."                   // required iff access_key set
//	}
//
// If `access_key` is omitted the AWS SDK's default credential chain is
// used: env vars (AWS_ACCESS_KEY_ID etc.), shared credentials file,
// IRSA / EKS pod identity, or EC2 instance profile. Production
// deployments should prefer that path over checking long-lived keys
// into the manager database.
//
// The token (when supplied via config) and zone fields are encrypted
// at rest by the column-level cipher already wrapping
// dns_providers.config; this struct never sees ciphertext.
type Route53 struct {
	ZoneID    string `json:"zone_id,omitempty"`
	ZoneName  string `json:"zone_name,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`

	// api lets tests substitute a fake. Production callers leave it
	// nil; NewRoute53 fills it from the AWS SDK.
	api route53API `json:"-"`
}

// route53API is the subset of *route53.Client our provider calls.
// Defining it as an interface keeps the provider testable without
// having to mock the whole SDK surface.
type route53API interface {
	ListHostedZonesByName(ctx context.Context, in *route53.ListHostedZonesByNameInput, opts ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
	ListResourceRecordSets(ctx context.Context, in *route53.ListResourceRecordSetsInput, opts ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ChangeResourceRecordSets(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, opts ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

// NewRoute53 parses the stored config and constructs a Route53
// provider. The AWS client is configured eagerly so config errors
// surface at the dns.Build call site (i.e. when the operator is still
// looking at their POST /dns_providers response).
func NewRoute53(rawConfig []byte) (*Route53, error) {
	var p Route53
	if err := json.Unmarshal(rawConfig, &p); err != nil {
		return nil, fmt.Errorf("route53: parse config: %w", err)
	}
	if p.ZoneID == "" && p.ZoneName == "" {
		return nil, errors.New("route53: zone_id or zone_name is required")
	}
	if p.AccessKey != "" && p.SecretKey == "" {
		return nil, errors.New("route53: secret_key is required when access_key is set")
	}

	cfg, err := buildAWSConfig(context.Background(), p)
	if err != nil {
		return nil, err
	}
	p.api = route53.NewFromConfig(cfg)
	return &p, nil
}

func buildAWSConfig(ctx context.Context, p Route53) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if p.Region != "" {
		opts = append(opts, awsconfig.WithRegion(p.Region))
	}
	if p.AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(p.AccessKey, p.SecretKey, ""),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("route53: load AWS config: %w", err)
	}
	return cfg, nil
}

// resolveZoneID returns the configured zone id, looking it up by name
// once if only zone_name was supplied. Route53's
// ListHostedZonesByName is paginated; we only need the first match
// because zone names are unique within an account.
func (p *Route53) resolveZoneID(ctx context.Context) (string, error) {
	if p.ZoneID != "" {
		return p.ZoneID, nil
	}
	out, err := p.api.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName:  aws.String(p.ZoneName),
		MaxItems: aws.Int32(1),
	})
	if err != nil {
		return "", fmt.Errorf("route53: list hosted zones: %w", err)
	}
	for _, z := range out.HostedZones {
		// Hosted zone names from the API have a trailing dot.
		if z.Name != nil && strings.TrimSuffix(*z.Name, ".") == p.ZoneName {
			// Zone IDs come back as "/hostedzone/Z2ABCDEFGHIJ" — strip
			// the prefix to match what ChangeResourceRecordSets expects.
			id := aws.ToString(z.Id)
			id = strings.TrimPrefix(id, "/hostedzone/")
			return id, nil
		}
	}
	return "", fmt.Errorf("route53: no hosted zone named %q", p.ZoneName)
}

// Present creates a TXT record. Route53's "UPSERT" action is
// idempotent — replays from the same value succeed without erroring,
// which matters for ACME flows that retry on transient failures.
func (p *Route53) Present(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	return p.changeRecord(ctx, zoneID, fqdn, value, types.ChangeActionUpsert)
}

// CleanUp removes the matching TXT record. We look it up first because
// Route53 requires the exact RR set the record currently holds in
// order to delete it (a TXT record can have multiple values).
func (p *Route53) CleanUp(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	// Confirm the record exists with the value we expect; if it doesn't,
	// silently succeed (a previous CleanUp may have removed it).
	out, err := p.api.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(zoneID),
		StartRecordName: aws.String(fqdn),
		StartRecordType: types.RRTypeTxt,
		MaxItems:        aws.Int32(1),
	})
	if err != nil {
		return fmt.Errorf("route53: list record set: %w", err)
	}
	if len(out.ResourceRecordSets) == 0 {
		return nil
	}
	rrset := out.ResourceRecordSets[0]
	if aws.ToString(rrset.Name) != fqdn || rrset.Type != types.RRTypeTxt {
		return nil
	}
	// route53 stores TXT values quoted; rebuild the exact RR set.
	return p.changeRecord(ctx, zoneID, fqdn, value, types.ChangeActionDelete)
}

func (p *Route53) changeRecord(ctx context.Context, zoneID, fqdn, value string, action types.ChangeAction) error {
	_, err := p.api.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{{
				Action: action,
				ResourceRecordSet: &types.ResourceRecordSet{
					Name: aws.String(fqdn),
					Type: types.RRTypeTxt,
					TTL:  aws.Int64(60),
					ResourceRecords: []types.ResourceRecord{{
						// Route53 requires TXT values to be quoted.
						Value: aws.String(`"` + value + `"`),
					}},
				},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("route53: %s record: %w", action, err)
	}
	return nil
}
