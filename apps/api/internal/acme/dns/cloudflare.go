package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Cloudflare is a DNS-01 provider backed by Cloudflare's REST API v4.
//
// Config JSON shape:
//
//	{
//	  "api_token":  "<scoped token with Zone.DNS:Edit on the zone>",
//	  "zone_id":    "abc123…",        // optional
//	  "zone_name":  "example.com"     // optional, used iff zone_id absent
//	}
//
// Either zone_id or zone_name must be set; if only zone_name is given,
// we look the zone up at startup. The API token must have at least
// Zone.DNS:Edit scope on the target zone.
type Cloudflare struct {
	APIToken string `json:"api_token"`
	ZoneID   string `json:"zone_id,omitempty"`
	ZoneName string `json:"zone_name,omitempty"`

	// BaseURL overrides https://api.cloudflare.com/client/v4 in tests.
	BaseURL string `json:"-"`
	// HTTPClient overrides http.DefaultClient in tests.
	HTTPClient *http.Client `json:"-"`
}

func NewCloudflare(rawConfig []byte) (*Cloudflare, error) {
	var p Cloudflare
	if err := json.Unmarshal(rawConfig, &p); err != nil {
		return nil, fmt.Errorf("cloudflare: parse config: %w", err)
	}
	if strings.TrimSpace(p.APIToken) == "" {
		return nil, errors.New("cloudflare: api_token is required")
	}
	if p.ZoneID == "" && p.ZoneName == "" {
		return nil, errors.New("cloudflare: zone_id or zone_name is required")
	}
	if p.BaseURL == "" {
		p.BaseURL = "https://api.cloudflare.com/client/v4"
	}
	return &p, nil
}

// cfResponse is the envelope every Cloudflare v4 endpoint wraps results in.
//
// We keep `Result` as json.RawMessage so each call site decodes its own
// shape; failure handling is shared.
type cfResponse struct {
	Success bool              `json:"success"`
	Errors  []cfMessage       `json:"errors"`
	Result  json.RawMessage   `json:"result"`
}

type cfMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r cfResponse) errorString() string {
	if len(r.Errors) == 0 {
		return "(no error message)"
	}
	parts := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		parts[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
	}
	return strings.Join(parts, "; ")
}

// resolveZoneID returns p.ZoneID, looking it up by name if necessary.
//
// The lookup runs once per Present/CleanUp call and is intentionally
// not cached — Cloudflare is fast enough that one extra round-trip per
// challenge is invisible, and avoiding cache invalidation is worth it.
func (p *Cloudflare) resolveZoneID(ctx context.Context) (string, error) {
	if p.ZoneID != "" {
		return p.ZoneID, nil
	}
	q := url.Values{"name": {p.ZoneName}}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := p.do(ctx, http.MethodGet, "/zones?"+q.Encode(), nil, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("cloudflare: no zone named %q", p.ZoneName)
	}
	return zones[0].ID, nil
}

// txtRecordName trims the trailing dot ACME challenges include — the
// Cloudflare API rejects the FQDN form.
func txtRecordName(fqdn string) string {
	return strings.TrimSuffix(fqdn, ".")
}

// Present creates a TXT record at fqdn with the given value.
func (p *Cloudflare) Present(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"type":    "TXT",
		"name":    txtRecordName(fqdn),
		"content": value,
		"ttl":     60,
	}
	return p.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", body, nil)
}

// CleanUp searches for matching TXT records and deletes them. Multiple
// records can match in flight (e.g. a previous failed run); we delete
// every one that matches both name and content.
func (p *Cloudflare) CleanUp(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	q := url.Values{
		"type":    {"TXT"},
		"name":    {txtRecordName(fqdn)},
		"content": {value},
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := p.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &records); err != nil {
		return err
	}
	for _, r := range records {
		if err := p.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+r.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// do is the shared request helper. result may be nil for endpoints we
// don't care about the body of (e.g. delete).
func (p *Cloudflare) do(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := p.HTTPClient
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("cloudflare %s %s: %s", method, path, resp.Status)
	}
	var env cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare %s %s: decode envelope: %w", method, path, err)
	}
	if !env.Success {
		return fmt.Errorf("cloudflare %s %s: %s", method, path, env.errorString())
	}
	if result != nil {
		if err := json.Unmarshal(env.Result, result); err != nil {
			return fmt.Errorf("cloudflare %s %s: decode result: %w", method, path, err)
		}
	}
	return nil
}
