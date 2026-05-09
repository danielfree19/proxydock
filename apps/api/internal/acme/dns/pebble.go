package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Pebble is a DNS-01 provider backed by Let's Encrypt's
// pebble-challtestsrv. The challtestsrv exposes a small HTTP API for
// adding and clearing TXT records that pebble (the test ACME CA) then
// queries during validation.
//
// Config JSON shape:
//
//	{ "base_url": "http://challtestsrv:8055" }
type Pebble struct {
	BaseURL    string       `json:"base_url"`
	HTTPClient *http.Client `json:"-"`
}

func NewPebble(rawConfig []byte) (*Pebble, error) {
	var p Pebble
	if err := json.Unmarshal(rawConfig, &p); err != nil {
		return nil, fmt.Errorf("pebble: parse config: %w", err)
	}
	if p.BaseURL == "" {
		return nil, fmt.Errorf("pebble: base_url is required")
	}
	return &p, nil
}

func (p *Pebble) Present(ctx context.Context, fqdn, value string) error {
	return p.post(ctx, "/set-txt", map[string]string{"host": fqdn, "value": value})
}

func (p *Pebble) CleanUp(ctx context.Context, fqdn, _ string) error {
	return p.post(ctx, "/clear-txt", map[string]string{"host": fqdn})
}

func (p *Pebble) post(ctx context.Context, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := p.HTTPClient
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("pebble-challtestsrv %s: %w", path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pebble-challtestsrv %s: %s", path, resp.Status)
	}
	return nil
}
