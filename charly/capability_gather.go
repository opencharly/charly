package main

// capability_gather.go — A-0: the §8.2 visibility gatherer that `spec/needsolver` documents but
// nothing provided, and the CALLER of `needsolver.Resolve`.
//
// Why this file exists: the solver decides WHICH provide a need is bound to; §8.2 decides what a
// node can SEE, and the solver's own doc puts that job here — "Visibility (§8.2) is the caller's to
// gather — the stack, the containing nodes, the alongside nodes, the registry, the grant — because
// only the caller knows the tree. This package resolves the set it is HANDED."
//
// THE REFUSAL IS THE DESIGN. The capability table (schema/capability.cue `_capability`) declares a
// `provide:` row per type, but no authored key writes one yet: a candy's only provides are the
// legacy env_provide / mcp_provide / agent_provide maps. So this gatherer carries an explicit
// TABLE of which capability types it can gather provides for TODAY, and a type whose row is absent
// raises ErrNoProvideSurface rather than returning an empty list. The distinction is D-CAP-3's own
// wording: an unbound need is "an error naming what was searched" — a search that CANNOT HAPPEN
// must not be reported as one that found nothing.
//
// When the authored `provide:` key lands (spec#368), exactly one row of `provideSurfaces` changes.

import (
	"fmt"
	"sort"

	"github.com/opencharly/spec/needsolver"
	"github.com/opencharly/spec/spec"
)

// ErrNoProvideSurface is the refusal: this capability type has no authored provide surface, so the
// gatherer cannot search for one. It is NOT ErrUnbound — that would claim a search happened.
type ErrNoProvideSurface struct {
	Type string

	// WantedBy names the node whose need raised this, so the error points at the author to fix.
	WantedBy string
}

func (e *ErrNoProvideSurface) Error() string {
	return fmt.Sprintf(
		"capability type %q has no authored provide surface: nothing can author a provide for it yet "+
			"(schema/capability.cue declares the row; no authored key writes one — spec#368), so the "+
			"need authored by %s cannot be searched for at all",
		e.Type, e.WantedBy)
}

// provideSurface collects the provides a stack member offers for ONE capability type.
type provideSurface struct {
	// collect returns the provides of `candy` for this type, or nil when it offers none. The
	// caller has already decided the type HAS a surface (it is in provideSurfaces).
	collect func(candy spec.Candy) []needsolver.Provide
}

// provideSurfaces is the table. A type absent from it cannot be gathered from: `env` is the one
// type with an authored provide surface today (`env_provide:`), and the rest are refused by name.
var provideSurfaces = map[string]provideSurface{
	"env": {collect: func(candy spec.Candy) []needsolver.Provide {
		names := make([]string, 0, len(candy.EnvProvides))
		for name := range candy.EnvProvides {
			names = append(names, name)
		}
		sort.Strings(names)
		out := make([]needsolver.Provide, 0, len(names))
		for _, name := range names {
			out = append(out, needsolver.Provide{
				Capability: needsolver.Capability{Type: "env", Name: name},
				Origin:     "candy:" + candy.Name,
			})
		}
		return out
	}},
}

// GatherStackCapabilities is A-0's entry point: it gathers every need and use authored by `owner`
// (a candy name) and resolves them against the provides of the stack members `visible` from it.
// The RESULT is the recorded need→provide map — the thing a type owner's `bind` will read
// `provided_by` from.
func GatherStackCapabilities(owner string, models map[string]spec.Candy, visible map[string]bool) ([]needsolver.Binding, error) {
	self, ok := models[owner]
	if !ok {
		return nil, fmt.Errorf("capability gather: %q is not in the resolved stack", owner)
	}

	// Wants: the owner's own `need:` and `use:` entries (§8.2 row 1 is where a candy's own entries
	// are bound; D-CAP-6's union across the deployment is the CALLER's to assemble, and arrives
	// here as one owner at a time).
	wants := append(wantsOfNeed(owner, self.Need), wantsOfUse(owner, self.Use)...)
	if len(wants) == 0 {
		return nil, nil
	}

	// The refusal comes FIRST, before any provide is gathered: a want whose type has no provide
	// surface fails the load loudly whether or not some lookalike provide sits in the tree.
	for _, w := range wants {
		if _, ok := provideSurfaces[w.Type]; !ok {
			return nil, &ErrNoProvideSurface{Type: w.Type, WantedBy: "candy:" + owner}
		}
	}

	// Provides: §8.2 rows 1-3 are the visible members; row 4 (the registry) and row 5 (the grant)
	// are the caller's to add to `visible` as their own entries. Gathered in sorted order so an
	// ambiguous error names both providers in stack order.
	members := make([]string, 0, len(visible))
	for name := range visible {
		members = append(members, name)
	}
	sort.Strings(members)

	// The provide TYPES to collect, as a SET. Collecting inside the want loop would gather a
	// member's provides once per want, so two env needs both provided by one candy would count
	// that candy's provide twice and Resolve would report a FALSE ErrAmbiguous naming
	// "(candy:prov, candy:prov)" -- an error that blames the tree for the gatherer's arithmetic.
	// One distinct type, one collection, regardless of how many wants share it.
	provTypes := make([]string, 0, len(provideSurfaces))
	seen := map[string]bool{}
	for _, w := range wants {
		if !seen[w.Type] {
			seen[w.Type] = true
			provTypes = append(provTypes, w.Type)
		}
	}
	sort.Strings(provTypes)

	var provides []needsolver.Provide
	for _, name := range members {
		m, ok := models[name]
		if !ok {
			return nil, fmt.Errorf("capability gather: visible member %q is not in the resolved stack", name)
		}
		for _, t := range provTypes {
			provides = append(provides, provideSurfaces[t].collect(m)...)
		}
	}

	return needsolver.Resolve(wants, provides)
}

// wantsOf projects one authored block (`need:` or `use:`) onto the solver's Want list. The entries
// are the capability table's typed maps: type -> name -> entry.
// wantsOf projects one node's authored capability entries into solver wants. The `need:` and
// `use:` halves differ in exactly two things -- the source struct and the Strength -- so the
// SIX-TYPE table and the sort-and-project loop live here ONCE. R2: a second copy of that table
// is a second place to forget a capability type when the schema grows one.
func wantsOf(owner string, byType map[string][]string, strength needsolver.Strength) []needsolver.Want {
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	var out []needsolver.Want
	for _, t := range types {
		for _, name := range byType[t] {
			out = append(out, needsolver.Want{
				Capability: needsolver.Capability{Type: t, Name: name},
				Strength:   strength,
				Authored:   "candy:" + owner,
			})
		}
	}
	return out
}

// wantsOfNeed is the `need:` half: D-CAP-1 says a need must be BOUND and fails the load when it
// is not.
func wantsOfNeed(owner string, need *spec.CandyNeed) []needsolver.Want {
	if need == nil {
		return nil
	}
	return wantsOf(owner, map[string][]string{
		"env":       capabilityKeysOf(need.Env),
		"parameter": capabilityKeysOf(need.Parameter),
		"secret":    capabilityKeysOf(need.Secret),
		"endpoint":  capabilityKeysOf(need.Endpoint),
		"network":   capabilityKeysOf(need.Network),
		"harness":   capabilityKeysOf(need.Harness),
	}, needsolver.Need)
}

// wantsOfUse is the `use:` half: the ones D-CAP-1 lets fall back when no provide is visible.
func wantsOfUse(owner string, use *spec.CandyUse) []needsolver.Want {
	if use == nil {
		return nil
	}
	return wantsOf(owner, map[string][]string{
		"env":       capabilityKeysOf(use.Env),
		"parameter": capabilityKeysOf(use.Parameter),
		"secret":    capabilityKeysOf(use.Secret),
		"endpoint":  capabilityKeysOf(use.Endpoint),
		"network":   capabilityKeysOf(use.Network),
		"harness":   capabilityKeysOf(use.Harness),
	}, needsolver.Use)
}

func capabilityKeysOf[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
