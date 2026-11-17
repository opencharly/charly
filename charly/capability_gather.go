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

	// The refusal comes FIRST, before any provide is gathered -- but it applies to `need:` wants
	// ONLY, and that asymmetry is D-CAP-1's, not a convenience:
	//
	//   "a `need:` must be bound and FAILS THE LOAD when it is not; a `use:` is bound when a
	//    provide exists and takes its `default:`, or GOES WITHOUT, otherwise."
	//
	// A `use:` of a type with no provide surface is the "goes without" case: it is RECORDED as
	// unbound (D-CAP-3) and the deployment proceeds. Raising here made a legitimate fallback
	// fatal, which is a worse failure than the silence this refusal exists to prevent.
	for _, w := range wants {
		if w.Strength != needsolver.Need {
			continue // a `use:` falls back; it never fails the load
		}
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
	// Only types that HAVE a surface are collected. A `use:` of a surface-less type survives the
	// refusal above (D-CAP-1: it goes without), so its type reaches this loop -- and the zero
	// value of provideSurface has a NIL `collect`, so including it would panic on the call rather
	// than record the unbound use. The surface check is the same one the refusal makes, from the
	// other side.
	provTypes := make([]string, 0, len(provideSurfaces))
	seen := map[string]bool{}
	for _, w := range wants {
		if _, hasSurface := provideSurfaces[w.Type]; !hasSurface {
			continue
		}
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
// authoredCapabilityTypes is the CLOSED set of capability types a node may author, in the order
// the schema declares them. It lives here ONCE and drives both halves: a new capability type is
// added to this slice and nowhere else, and wantsOf checks that each caller supplies exactly this
// many key lists -- so a type added to the schema without a projection is a runtime refusal at
// the first gather, not a silent omission.
var authoredCapabilityTypes = []string{"env", "parameter", "secret", "endpoint", "network", "harness"}

// wantsOf projects one node's authored capability entries into solver wants. The `need:` and
// `use:` halves differ in exactly two things -- the values and the Strength -- so the type NAMES
// live in authoredCapabilityTypes and the values arrive positionally, one list per type in that
// order. R2: the table was copy-pasted once and the body claimed otherwise; it is one table now.
func wantsOf(owner string, keys [][]string, strength needsolver.Strength) []needsolver.Want {
	if len(keys) != len(authoredCapabilityTypes) {
		panic(fmt.Sprintf("capability gather: %d key lists for %d authored types -- a capability type "+
			"was added to the schema without a projection; add it to authoredCapabilityTypes and to "+
			"every caller", len(keys), len(authoredCapabilityTypes)))
	}
	var out []needsolver.Want
	for i, t := range authoredCapabilityTypes {
		for _, name := range keys[i] {
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
// is not. The key lists are positional, one per authoredCapabilityTypes entry.
func wantsOfNeed(owner string, need *spec.CandyNeed) []needsolver.Want {
	if need == nil {
		return nil
	}
	return wantsOf(owner, [][]string{
		capabilityKeysOf(need.Env),
		capabilityKeysOf(need.Parameter),
		capabilityKeysOf(need.Secret),
		capabilityKeysOf(need.Endpoint),
		capabilityKeysOf(need.Network),
		capabilityKeysOf(need.Harness),
	}, needsolver.Need)
}

// wantsOfUse is the `use:` half: the ones D-CAP-1 lets fall back when no provide is visible.
func wantsOfUse(owner string, use *spec.CandyUse) []needsolver.Want {
	if use == nil {
		return nil
	}
	return wantsOf(owner, [][]string{
		capabilityKeysOf(use.Env),
		capabilityKeysOf(use.Parameter),
		capabilityKeysOf(use.Secret),
		capabilityKeysOf(use.Endpoint),
		capabilityKeysOf(use.Network),
		capabilityKeysOf(use.Harness),
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
