// Package eval is the deterministic rules engine.
//
// Given a Project Profile and a compiled ruleset, it produces a Determination:
// the applicable approvals, the deduplicated document set, the computed fees,
// the dependency DAG, the obligations, the matched incentives, the questions
// that remain unanswered, and a full derivation trace.
//
// Three properties are load-bearing and are enforced by construction:
//
//	Deterministic  Nothing here reads the clock, a random source, the network,
//	               or a map in iteration order. The same profile and the same
//	               ruleset version always produce byte-identical output (P1).
//	Total          Every expression evaluates to a value or to UNKNOWN. There is
//	               no failure mode in which the engine has no answer.
//	Honest         When the profile is insufficient, the engine says so and
//	               names the question to ask. It never guesses and never
//	               silently omits a requirement (spec 5.5.4).
package eval

import (
	"sort"

	"github.com/setu-platform/setu/pkg/value"
)

// Profile is an applicant's declaration: the sole input to discovery.
type Profile struct {
	// ID identifies the profile for provenance in a determination.
	ID string
	// Version is the profile revision this determination was computed from.
	Version int
	// Values holds the answers given so far. A variable absent from the map is
	// unanswered, which is a first-class state, not a zero value.
	Values map[string]value.Value
}

// NewProfile constructs an empty profile.
func NewProfile(id string) *Profile {
	return &Profile{ID: id, Version: 1, Values: map[string]value.Value{}}
}

// Set records an answer and returns the profile, for fluent construction.
func (p *Profile) Set(name string, v value.Value) *Profile {
	p.Values[name] = v
	return p
}

// Get returns the answer for a variable, or the unknown value.
func (p *Profile) Get(name string) value.Value {
	if p == nil {
		return value.Null
	}
	v, ok := p.Values[name]
	if !ok {
		return value.Null
	}
	return v
}

// Answered reports whether a variable has an answer.
func (p *Profile) Answered(name string) bool {
	if p == nil {
		return false
	}
	v, ok := p.Values[name]
	return ok && !v.IsUnknown()
}

// Names lists answered variables in sorted order.
func (p *Profile) Names() []string {
	out := make([]string, 0, len(p.Values))
	for k := range p.Values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Clone returns a deep-enough copy for what-if scenarios, which must not
// mutate the profile they were derived from.
func (p *Profile) Clone() *Profile {
	c := &Profile{ID: p.ID, Version: p.Version, Values: make(map[string]value.Value, len(p.Values))}
	for k, v := range p.Values {
		c.Values[k] = v
	}
	return c
}

// Input is everything an evaluation depends on besides the ruleset.
type Input struct {
	Profile *Profile

	// AsOf anchors effective dating. It is the date the case was submitted, or
	// today for an exploratory determination, and is always supplied by the
	// caller: the engine never reads the clock, because a determination must be
	// reproducible years later (P6).
	AsOf value.Date

	// Language selects which localised explanation is rendered.
	Language string

	// Layers are the versioned boundary layers geospatial predicates resolve
	// against. Pinning them alongside the ruleset is what makes
	// "within the coastal regulation zone" reproducible.
	Layers *value.Layers
}
