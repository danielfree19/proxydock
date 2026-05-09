package labels

import (
	"strings"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"region=us", "region=us"},
		{"region=us,tier=prod", "region=us,tier=prod"},
		{"  tier = prod , region = us ", "region=us,tier=prod"},
	}
	for _, tc := range cases {
		s, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got := s.String(); got != tc.want {
			t.Fatalf("Parse(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct{ in, contains string }{
		{"keyOnly", "must be key=value"},
		{"=value", "empty key"},
		{"region=us,region=eu", "contradictory"},
	}
	for _, tc := range cases {
		_, err := Parse(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.contains) {
			t.Fatalf("Parse(%q): err=%v want substring %q", tc.in, err, tc.contains)
		}
	}
}

func TestMatches_Empty(t *testing.T) {
	s, _ := Parse("")
	if !s.Matches(nil) {
		t.Fatal("empty selector should match nil labels")
	}
	if !s.Matches([]string{"region=us"}) {
		t.Fatal("empty selector should match any labels")
	}
}

func TestMatches_AndedEquality(t *testing.T) {
	s, _ := Parse("region=us,tier=prod")
	if !s.Matches([]string{"region=us", "tier=prod", "irrelevant=x"}) {
		t.Fatal("expected match")
	}
	if s.Matches([]string{"region=us"}) {
		t.Fatal("missing requirement should fail")
	}
	if s.Matches([]string{"region=eu", "tier=prod"}) {
		t.Fatal("wrong value should fail")
	}
	if s.Matches(nil) {
		t.Fatal("no labels should not match a non-empty selector")
	}
}

func TestMatches_IgnoresMalformedAgentLabels(t *testing.T) {
	s, _ := Parse("region=us")
	// One label is malformed (no =) — it should just be ignored, not break.
	if !s.Matches([]string{"orphan", "region=us"}) {
		t.Fatal("expected match in spite of orphan label")
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("region=us"); err != nil {
		t.Fatal(err)
	}
	if err := Validate("garbage"); err == nil {
		t.Fatal("expected validation error")
	}
}
