package dns

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// fakeRoute53 records every call and returns canned data. It satisfies
// the route53API interface so we can swap it in for *route53.Client.
type fakeRoute53 struct {
	mu sync.Mutex

	// Hosted zones: name (trailing-dot, as Route53 returns it) -> id.
	zones map[string]string

	// Existing records keyed by (zoneID, name, type).
	records map[string]map[string]string // zone -> fqdn -> value

	calls []string
}

func newFake(zones, records map[string]string) *fakeRoute53 {
	rec := map[string]map[string]string{}
	for fqdn, val := range records {
		// All records belong to the only zone in tests.
		zone := ""
		for _, id := range zones {
			zone = id
			break
		}
		if rec[zone] == nil {
			rec[zone] = map[string]string{}
		}
		rec[zone][fqdn] = val
	}
	return &fakeRoute53{zones: zones, records: rec}
}

func (f *fakeRoute53) ListHostedZonesByName(_ context.Context, in *route53.ListHostedZonesByNameInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ListHostedZonesByName "+aws.ToString(in.DNSName))
	out := &route53.ListHostedZonesByNameOutput{}
	for name, id := range f.zones {
		if strings.TrimSuffix(name, ".") == aws.ToString(in.DNSName) {
			out.HostedZones = append(out.HostedZones, types.HostedZone{
				Id:   aws.String("/hostedzone/" + id),
				Name: aws.String(name),
			})
		}
	}
	return out, nil
}

func (f *fakeRoute53) ListResourceRecordSets(_ context.Context, in *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ListResourceRecordSets "+aws.ToString(in.HostedZoneId)+" "+aws.ToString(in.StartRecordName))
	out := &route53.ListResourceRecordSetsOutput{}
	if zone := f.records[aws.ToString(in.HostedZoneId)]; zone != nil {
		if val, ok := zone[aws.ToString(in.StartRecordName)]; ok {
			out.ResourceRecordSets = append(out.ResourceRecordSets, types.ResourceRecordSet{
				Name: in.StartRecordName,
				Type: types.RRTypeTxt,
				TTL:  aws.Int64(60),
				ResourceRecords: []types.ResourceRecord{{
					Value: aws.String(`"` + val + `"`),
				}},
			})
		}
	}
	return out, nil
}

func (f *fakeRoute53) ChangeResourceRecordSets(_ context.Context, in *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone := aws.ToString(in.HostedZoneId)
	for _, ch := range in.ChangeBatch.Changes {
		fqdn := aws.ToString(ch.ResourceRecordSet.Name)
		val := aws.ToString(ch.ResourceRecordSet.ResourceRecords[0].Value)
		val = strings.Trim(val, `"`)
		f.calls = append(f.calls, string(ch.Action)+" "+zone+" "+fqdn+"="+val)
		switch ch.Action {
		case types.ChangeActionUpsert, types.ChangeActionCreate:
			if f.records[zone] == nil {
				f.records[zone] = map[string]string{}
			}
			f.records[zone][fqdn] = val
		case types.ChangeActionDelete:
			if f.records[zone] != nil {
				delete(f.records[zone], fqdn)
			}
		}
	}
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}

func newRoute53WithFake(t *testing.T, zoneID, zoneName string, fake *fakeRoute53) *Route53 {
	t.Helper()
	p := &Route53{ZoneID: zoneID, ZoneName: zoneName, api: fake}
	return p
}

func TestRoute53_Present_ZoneID(t *testing.T) {
	fake := newFake(map[string]string{"example.com.": "Z123"}, nil)
	p := newRoute53WithFake(t, "Z123", "", fake)
	ctx := context.Background()
	if err := p.Present(ctx, "_acme-challenge.example.com.", "v1"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.records["Z123"]["_acme-challenge.example.com."], "v1"; got != want {
		t.Fatalf("record value = %q, want %q", got, want)
	}
	// Direct zone_id should skip ListHostedZonesByName entirely.
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "ListHostedZonesByName") {
			t.Fatalf("zone_id supplied but provider still looked up by name: %v", fake.calls)
		}
	}
}

func TestRoute53_Present_ZoneName_Lookup(t *testing.T) {
	fake := newFake(map[string]string{"example.com.": "Z123"}, nil)
	p := newRoute53WithFake(t, "", "example.com", fake)
	ctx := context.Background()
	if err := p.Present(ctx, "_acme-challenge.example.com.", "v1"); err != nil {
		t.Fatal(err)
	}
	sawLookup := false
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "ListHostedZonesByName example.com") {
			sawLookup = true
		}
	}
	if !sawLookup {
		t.Fatalf("expected a zone lookup, got: %v", fake.calls)
	}
	if got := fake.records["Z123"]["_acme-challenge.example.com."]; got != "v1" {
		t.Fatalf("record not stored under resolved zone: %v", fake.records)
	}
}

func TestRoute53_CleanUp_DeletesMatching(t *testing.T) {
	fake := newFake(
		map[string]string{"example.com.": "Z123"},
		map[string]string{"_acme-challenge.example.com.": "v1"},
	)
	p := newRoute53WithFake(t, "Z123", "", fake)
	ctx := context.Background()
	if err := p.CleanUp(ctx, "_acme-challenge.example.com.", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, still := fake.records["Z123"]["_acme-challenge.example.com."]; still {
		t.Fatalf("CleanUp didn't delete: %v", fake.records)
	}
}

func TestRoute53_CleanUp_MissingIsNotAnError(t *testing.T) {
	fake := newFake(map[string]string{"example.com.": "Z123"}, nil)
	p := newRoute53WithFake(t, "Z123", "", fake)
	if err := p.CleanUp(context.Background(),
		"_acme-challenge.example.com.", "v1"); err != nil {
		t.Fatalf("CleanUp on missing record errored: %v", err)
	}
}

func TestRoute53_NewRoute53_ConfigValidation(t *testing.T) {
	if _, err := NewRoute53([]byte(`{}`)); err == nil {
		t.Fatal("expected zone_id/zone_name error")
	}
	if _, err := NewRoute53([]byte(`{"zone_id":"Z","access_key":"AKIA"}`)); err == nil {
		t.Fatal("expected secret_key error")
	}
}

func TestRoute53_BuildViaRegistry(t *testing.T) {
	p, err := Build("route53", []byte(`{"zone_id":"Z","region":"us-east-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Route53); !ok {
		t.Fatalf("got %T, want *Route53", p)
	}
}
