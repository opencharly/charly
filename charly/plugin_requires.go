package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/opencharly/spec/spec"
	pb "github.com/opencharly/spec/proto"
)

// plugin_requires.go — the F-A3 DECLARED inter-plugin dependency gate. A plugin
// advertises the OTHER plugins it depends on over Describe (the wire
// Capabilities.requires field, authored as the candy `plugin.requires:` list and
// marshalled by sdk.NewMetaWithRequires / BuildCapabilitiesWithRequires). The host
// resolves every requirement against the provider registry — connecting a missing
// peer declaratively via the SAME lazy-connect chain a call-time ExtraRef drives —
// so a plugin's internal peer need no longer depends on the peer being referenced
// by the project's own plans. Applied identically in every placement (compiled-in,
// project-declared external, demand/baked): the gate runs wherever a unit is
// registered.
//
// A non-optional requirement that cannot be resolved is a HARD error naming the
// plugin and the missing peer (the same loud-at-load contract as the schema gate);
// `optional: true` records a skip instead. A declared dependency CYCLE is rejected
// with the chain named.
//
// The unit holds the CUE-GENERATED spec.PluginRequirement (the authored `#PluginRequirement`
// shape — capability + source + optional), not a hand-written mirror: `capability` is the
// peer's full IDENTITY `<class>:<word>[:<parent>]` (the C0 key grammar), parsed with the ONE
// `splitCapability`.

// liftRequirements converts a unit's wire requirements to the CUE-authored
// spec.PluginRequirement shape, validating the class/word with the SAME closed class set
// the capability lift uses (one vocabulary, R3). The wire splits the peer into
// class/word/command_parent; the authored shape carries the ONE capability identity, so
// this re-renders it with providerKey (the ONE key renderer).
func liftRequirements(required []*pb.PluginRequirement, origin string) ([]spec.PluginRequirement, error) {
	if len(required) == 0 {
		return nil, nil
	}
	out := make([]spec.PluginRequirement, 0, len(required))
	for _, r := range required {
		class := ProviderClass(r.GetClass())
		if !providerClasses[class] || r.GetWord() == "" {
			return nil, fmt.Errorf("%s advertised malformed requirement %q:%q", origin, r.GetClass(), r.GetWord())
		}
		out = append(out, spec.PluginRequirement{
			Capability: spec.PluginCapability(providerKey(class, r.GetWord(), r.GetCommandParent())),
			Source:     r.GetSource(),
			Optional:   r.GetOptional(),
		})
	}
	return out, nil
}

// requiresInFlight maps a provider key ("<class>:<word>") to the unit name currently
// resolving its OWN requirements. It exists only to detect a declared dependency CYCLE
// with a readable chain; it is not a cache. Guarded by requiresGateMu.
var (
	requiresGateMu   sync.Mutex
	requiresInFlight = map[string]string{}
)

// registerPluginUnitRequires is THE declared-dependency gate: called wherever a plugin
// unit is registered (compiled-in at process start, external at connect, baked at lazy
// connect), it resolves every requirement before the unit is considered loaded.
//
//   - an already-registered peer resolves immediately (no reconnect);
//   - a missing peer is connected on demand via connectPluginByWordRef with the
//     requirement's `source` as the canonical ref (the SAME S2/S3b chain a call-time
//     ExtraRef uses);
//   - a requirement whose peer is currently resolving its OWN requirements is a
//     declared CYCLE, rejected naming the chain;
//   - an unresolvable NON-optional requirement is a hard error naming the plugin and
//     the peer; `optional: true` warns and continues.
func registerPluginUnitRequires(unitName string, unit *PluginUnit) error {
	if unit == nil || len(unit.Requires) == 0 {
		return nil
	}
	// Mark this unit's own provider words in flight for the duration, so a requirement
	// that resolves back to a unit being connected is detected as a cycle rather than
	// recursing.
	own := make([]string, 0, len(unit.Providers))
	requiresGateMu.Lock()
	for _, p := range unit.Providers {
		k := provKey(p.Class(), p.Reserved())
		if prev, ok := requiresInFlight[k]; ok && prev != unitName {
			requiresGateMu.Unlock()
			return fmt.Errorf("plugin dependency cycle: %s and %s both provide %s", prev, unitName, k)
		}
		own = append(own, k)
		requiresInFlight[k] = unitName
	}
	requiresGateMu.Unlock()
	defer func() {
		requiresGateMu.Lock()
		for _, k := range own {
			delete(requiresInFlight, k)
		}
		requiresGateMu.Unlock()
	}()

	for _, req := range unit.Requires {
		class, word, parent, ok := splitCapability(string(req.Capability))
		if !ok {
			return fmt.Errorf("plugin %q declared malformed capability %q", unitName, req.Capability)
		}
		k := providerKey(class, word, parent)
		requiresGateMu.Lock()
		owner := requiresInFlight[k]
		requiresGateMu.Unlock()
		if owner != "" && owner != unitName {
			return fmt.Errorf("plugin dependency cycle: %s requires %s, which is provided by %s while %s is still loading", unitName, k, owner, owner)
		}
		if _, ok := providerRegistry.resolveIdentity(class, word, parent); ok {
			continue
		}
		if _, ok := connectPluginByWordRef(class, word, parent, req.Source); ok {
			continue
		}
		if req.Optional {
			fmt.Fprintf(os.Stderr, "warning: plugin %q: optional dependency %s not available (skipped)\n", unitName, k)
			continue
		}
		hint := "add its candy to the project's closure"
		if req.Source != "" {
			hint = "its declared source " + req.Source
		}
		return fmt.Errorf("plugin %q requires %s, which is not registered and could not be connected (%s)", unitName, k, hint)
	}
	return nil
}
