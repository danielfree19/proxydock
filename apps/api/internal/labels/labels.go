// Package labels parses the label-selector mini-language used to target
// proxy hosts at a subset of agents.
//
// Selector format (Kubernetes-style equality match, ANDed):
//
//	"key=value,key2=value2"
//
// An empty or whitespace-only selector matches every agent. A
// non-empty selector requires every agent.Labels entry of the form
// "key=value" to satisfy the corresponding requirement; one mismatch
// rejects the host.
//
// Phase 5b deliberately implements only equality matching. Set-based
// matchers (`key in (a,b)`, `key!=value`, `!key`) can be added by
// extending Selector.parse without changing the rest of the codebase.
package labels

import (
	"fmt"
	"sort"
	"strings"
)

// Selector is a parsed label selector.
type Selector struct {
	requirements map[string]string
}

// Parse turns a raw selector string into a Selector value.
func Parse(raw string) (*Selector, error) {
	s := &Selector{requirements: map[string]string{}}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s, nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("labels: %q: each requirement must be key=value", part)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("labels: %q: empty key", part)
		}
		if existing, dup := s.requirements[k]; dup && existing != v {
			return nil, fmt.Errorf("labels: %q: contradictory requirements for %q", raw, k)
		}
		s.requirements[k] = v
	}
	return s, nil
}

// Empty reports whether the selector imposes no requirements (matches
// every agent).
func (s *Selector) Empty() bool {
	if s == nil {
		return true
	}
	return len(s.requirements) == 0
}

// Matches returns true iff every requirement is satisfied by an entry
// of agentLabels of the form "key=value".
func (s *Selector) Matches(agentLabels []string) bool {
	if s.Empty() {
		return true
	}
	have := map[string]string{}
	for _, l := range agentLabels {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		have[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	for k, want := range s.requirements {
		if got, ok := have[k]; !ok || got != want {
			return false
		}
	}
	return true
}

// String returns a canonical, deterministic re-encoding of the selector.
// Useful for tests and for re-serialising user input back into the DB.
func (s *Selector) String() string {
	if s.Empty() {
		return ""
	}
	keys := make([]string, 0, len(s.requirements))
	for k := range s.requirements {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+s.requirements[k])
	}
	return strings.Join(parts, ",")
}

// Validate parses raw without keeping the selector — useful from
// HTTP handlers that just want to reject bad input.
func Validate(raw string) error {
	_, err := Parse(raw)
	return err
}
