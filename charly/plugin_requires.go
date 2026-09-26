package main

import (
	"fmt"
	"os"
	"sync"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// plugin_requires.go — the DECLARED inter-plugin dependency gate. A plugin declares the
// OTHER plugins it depends on as the authored `plugin.requires:` list in its candy
// manifest; the host reads that declaration from the resolved view
// (spec.CandyReader.GetPluginRequires) and resolves every requirement against the provider
// registry — connecting a missing peer declaratively via the SAME lazy-connect chain a
// call-time ExtraRef drives — so a plugin's internal peer need no longer depend on the peer
// being referenced by the project's own plans.
//
// The manifest is THE authored source of the declaration, read wherever a scanned candy is
// in scope: the project-declared external connect (loadPluginUnit, the requires threaded in
// by loadProjectPlugins) AND the compiled-in candy (buildUnitInProc's candy, gated in
// loadProjectPlugins). The served wire Capabilities.requires
// (sdk.NewMetaWithRequires / BuildCapabilitiesWithRequires) is the SAME fact on the
// transport for the placements that carry NO manifest — the process-start builtin gate and
// a source-less baked binary — and those keep reading the wire.
//
// A non-optional requirement that cannot be resolved is a HARD error naming the plugin and
// the missing peer (the same loud-at-load contract as the schema gate); `optional: true`
// records a skip instead. A declared dependency CYCLE is rejected with the chain named.
//
// The gate holds the CUE-GENERATED spec.PluginRequirement (the authored `#PluginRequirement`
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

// registerPluginUnitRequires gates a connected plugin UNIT's WIRE-declared requirements —
// the placements that carry no manifest: the process-start builtin gate
// (loadBuiltinPluginUnits) and a source-less baked binary (loadBakedPluginBinary). It
// derives the unit's own provider identities and delegates to registerPluginRequires. A
// scanned candy's AUTHORED manifest declaration enters through gateCandyRequires instead.
func registerPluginUnitRequires(unitName string, unit *PluginUnit) error {
	if unit == nil {
		return nil
	}
	return registerPluginRequires(unitName, unitProviderKeys(unit), unit.Requires)
}

// unitProviderKeys renders a connected unit's provider identities (the ONE registry key
// grammar) for the gate's cycle-detection marking. A nested command keeps its declared
// parent — providerIdentity, not the two-segment provKey — so a requirement naming the
// parented identity is matched against the in-flight unit exactly as the registry keys it.
func unitProviderKeys(unit *PluginUnit) []string {
	keys := make([]string, 0, len(unit.Providers))
	for _, p := range unit.Providers {
		keys = append(keys, providerIdentity(p))
	}
	return keys
}

// gateCandyRequires gates a scanned plugin candy's AUTHORED manifest declaration
// (`plugin.requires:`, read from the resolved view). It derives the candy's own provider
// identities for cycle detection and resolves every requirement. This is the ONE entry
// point for the manifest declaration, called for a project-declared external candy (before
// its unit is registered) and for a compiled-in candy (whose unit is already registered at
// init()). The wire Capabilities.requires is NOT consulted here: the manifest supersedes it
// as THE declaration wherever the candy is in scope.
func gateCandyRequires(name string, candy spec.CandyReader) error {
	if candy == nil {
		return nil
	}
	requires := candy.GetPluginRequires()
	if len(requires) == 0 {
		return nil
	}
	providers := candy.GetPluginProviders()
	own := make([]string, 0, len(providers))
	for _, capability := range providers {
		class, word, parent, ok := splitCapability(capability)
		if !ok {
			continue
		}
		own = append(own, providerKey(class, word, parent))
	}
	return registerPluginRequires(name, own, requires)
}

// registerPluginRequires is the ONE declared-dependency gate body, source-agnostic: it
// resolves each requirement against the registry, connecting a missing peer on demand
// (connectPluginByWordRef with the requirement's `source`), rejecting a cycle naming the
// chain, and failing loudly (or warning for `optional`) on an unresolvable peer. ownKeys are
// the DECLARING unit's own provider identities, marked in flight so a requirement resolving
// back to a unit being loaded is a cycle rather than a recursion.
//
//   - an already-registered peer resolves immediately (no reconnect);
//   - a missing peer is connected on demand via connectPluginByWordRef with the
//     requirement's `source` as the canonical ref (the SAME S2/S3b chain a call-time
//     ExtraRef uses);
//   - a requirement whose peer is currently resolving its OWN requirements is a
//     declared CYCLE, rejected naming the chain;
//   - an unresolvable NON-optional requirement is a hard error naming the plugin and
//     the peer; `optional: true` warns and continues.
func registerPluginRequires(unitName string, ownKeys []string, requires []spec.PluginRequirement) error {
	if len(requires) == 0 {
		return nil
	}
	// Mark the declaring unit's own provider words in flight for the duration, so a
	// requirement that resolves back to a unit being connected is detected as a cycle
	// rather than recursing.
	requiresGateMu.Lock()
	for _, k := range ownKeys {
		if prev, ok := requiresInFlight[k]; ok && prev != unitName {
			requiresGateMu.Unlock()
			return fmt.Errorf("plugin dependency cycle: %s and %s both provide %s", prev, unitName, k)
		}
		requiresInFlight[k] = unitName
	}
	requiresGateMu.Unlock()
	defer func() {
		requiresGateMu.Lock()
		for _, k := range ownKeys {
			delete(requiresInFlight, k)
		}
		requiresGateMu.Unlock()
	}()

	for _, req := range requires {
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
