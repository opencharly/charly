package main

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// providerRegistry is the ONE process-wide registry of Providers — the unified
// dispatch surface the per-class built-in switches register into (kinds, verbs,
// and deploy targets so far; VerbCatalog in reserved_registry.go remains the
// verb-metadata map). An out-of-process live-container verb owns its own method
// allowlist + required-modifier checks in its plugin (candy/plugin-*), enforced by
// CUE on core #Op — there is no in-proc method-contract seam in charly anymore.
// Built-ins register from init() (RegisterBuiltinProvider); plugins register
// lazily after the loader connects them (RegisterPluginProviders). Every reserved
// word resolves through here regardless of transport.
//
// Every built-in dispatch is registry-driven: VERBS (C1), KINDS (C2), STEPS (C4),
// BUILDERS (C5), and DEPLOY TARGETS (C3) all register here and their dispatch
// switches are DELETED — the call site resolves + dispatches through the registry.
// The registry also holds every plugin-contributed provider (its first consumer was
// the example plugin in C0).
var providerRegistry = newRegistry()

// Registry maps (class, reserved-word) → Provider. Keyed by both because a word
// may exist in two classes (e.g. "k8s" is both a kind and a verb).
type Registry struct {
	mu      sync.RWMutex
	byKey   map[string]Provider
	origins map[string]string // key → "builtin" | "github.com/org/repo@tag" | "local:<bin>"
	closers []io.Closer       // plugin connections, closed by Close()
}

func newRegistry() *Registry {
	return &Registry{byKey: map[string]Provider{}, origins: map[string]string{}}
}

func provKey(c ProviderClass, word string) string { return string(c) + ":" + word }

// register indexes one provider. It is the single mutation path (R3): it rejects
// an unknown class and a duplicate (class, word) — fail-fast, like
// registerCueKind's duplicate panic.
func (r *Registry) register(p Provider, origin string) error {
	class, word := p.Class(), p.Reserved()
	if !providerClasses[class] {
		return fmt.Errorf("provider %q: unknown class %q", word, class)
	}
	if word == "" {
		return fmt.Errorf("provider (class %q): empty reserved word", class)
	}
	if class == ClassVerb {
		// The schema-compaction collision gate: a plugin verb word that equals an
		// authored #Op field could never be reached by the parse-time sugar rule
		// (the key would classify as a builtin modifier), so registering one is a
		// hard error at the source.
		if authoredOpFieldSet[word] {
			return fmt.Errorf("provider verb word %q collides with an authored #Op field — the `<word>: <input>` sugar could never dispatch it; pick a non-colliding word", word)
		}
		// A declared scalar-sugar primary registers into the parse-time desugar.
		if pc, ok := p.(primaryCarrier); ok {
			if prim := pc.primaryInput(); prim != "" {
				if err := registerPluginPrimary(word, prim); err != nil {
					return err
				}
			}
		}
	}
	k := provKey(class, word)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byKey[k]; dup {
		return fmt.Errorf("provider %s already registered (origin %s) — refusing duplicate from %s",
			k, r.origins[k], origin)
	}
	r.byKey[k] = p
	r.origins[k] = origin
	return nil
}

// registeredOrigin reports the origin a (class, word) provider is registered from,
// if any. It lets loadProjectPlugins make a same-origin re-load IDEMPOTENT (skip the
// whole build+connect+schema-append+register) while still surfacing a different-origin
// collision — WITHOUT touching register, which stays the fail-fast bijection backstop.
// Returns ("", false) for an unregistered word.
func (r *Registry) registeredOrigin(class ProviderClass, word string) (string, bool) {
	k := provKey(class, word)
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.origins[k]
	return o, ok
}

// originBuiltin is the registry origin tag for an in-process provider compiled into
// charly — a core builtin (RegisterBuiltinProvider) OR a plugin candy compiled in via
// the charly.yml compiled_plugins selection (registerCompiledPlugin). The coexist
// switch in pluginAlreadyConnected keys on it to skip the redundant out-of-process
// build+connect for an already-compiled-in word.
const originBuiltin = "builtin"

// RegisterBuiltinProvider is called from init() for an in-process built-in. It
// panics on conflict (a startup invariant, like the bijection gate) — a built-in
// duplicate is a programming error caught at process start.
func RegisterBuiltinProvider(p Provider) {
	if err := providerRegistry.register(p, originBuiltin); err != nil {
		panic("RegisterBuiltinProvider: " + err.Error())
	}
}

// builtinPluginUnits holds every in-tree plugin UNIT registered from init() (a
// candy with a `plugin: { source: builtin }` block — its providers + its embedded
// self-contained CUE schema). It is the in-proc analogue of an external plugin's
// served unit: loadBuiltinPluginUnits gates every one of these schemas at process
// start through the SAME gate an external goes through.
// Core C1–C5 providers (cdp/box/local/…) are NOT units — their params live in the
// base schema (#Op/#Box), so they register via RegisterBuiltinProvider; only
// `plugin:`-block candies contribute a splice-on unit.
var builtinPluginUnits []PluginUnit

// RegisterBuiltinPluginUnit registers a built-in plugin unit from init(): it
// indexes the unit (so loadBuiltinPluginUnits can gate its schema) AND registers
// every provider (panicking on conflict, a startup invariant like
// RegisterBuiltinProvider — a built-in provider is available immediately, like a
// core provider; only its schema gating is deferred to the process-start pass).
func RegisterBuiltinPluginUnit(u PluginUnit) {
	builtinPluginUnits = append(builtinPluginUnits, u)
	for _, p := range u.Providers {
		if err := providerRegistry.register(p, originBuiltin); err != nil {
			panic("RegisterBuiltinPluginUnit: " + err.Error())
		}
	}
}

// RegisterPluginProviders indexes the (already-connected) out-of-process
// providers of one plugin, tracking the connection for Close(). Unlike a built-in
// it returns an error rather than panicking — a misbehaving third-party plugin
// must not crash charly; the loader surfaces the error and skips the plugin.
func (r *Registry) RegisterPluginProviders(ps []Provider, origin string, conn io.Closer) error {
	for _, p := range ps {
		if err := r.register(p, origin); err != nil {
			return err
		}
		// F6: a class:deploy provider declaring a venue lifecycle gets a wire-backed
		// substrateLifecycle registered for its word, so externalDeployTarget (which calls only
		// through the substrateLifecycle interface) drives the plugin's lifecycle transparently.
		if gp, ok := p.(*grpcProvider); ok && gp.class == ClassDeployTarget {
			if gp.lifecycle {
				registerPluginSubstrateLifecycle(gp.word, grpcSubstrateLifecycle{prov: gp})
			}
			if gp.preresolve {
				registerPluginDeployPreresolver(gp.word, wireDeployPreresolver(gp))
			}
		}
	}
	if conn != nil {
		r.mu.Lock()
		r.closers = append(r.closers, conn)
		r.mu.Unlock()
	}
	return nil
}

// resolve returns the provider for (class, word).
func (r *Registry) resolve(class ProviderClass, word string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.byKey[provKey(class, word)]; ok {
		return p, true
	}
	return nil, false
}

// Typed resolvers — what the call sites use. They never branch on transport.
func (r *Registry) ResolveVerb(word string) (Provider, bool) { return r.resolve(ClassVerb, word) }
func (r *Registry) ResolveKind(word string) (Provider, bool) { return r.resolve(ClassKind, word) }
func (r *Registry) ResolveDeploy(word string) (Provider, bool) {
	return r.resolve(ClassDeployTarget, word)
}
func (r *Registry) ResolveStep(word string) (Provider, bool)    { return r.resolve(ClassStep, word) }
func (r *Registry) ResolveBuilder(word string) (Provider, bool) { return r.resolve(ClassBuilder, word) }

// allServedUnits expresses every in-proc provider as PluginUnits for
// `charly __plugin serve`: each builtin plugin unit (carrying its self-contained
// schema) plus a single schema-less unit wrapping the remaining core providers
// (cdp/box/local/… — their params live in the base #Op/#Box, not a splice-on
// schema). So a charly served out-of-process advertises plugin schemas over
// Describe byte-identically to an external plugin.
func (r *Registry) allServedUnits() []PluginUnit {
	inUnit := map[string]bool{}
	units := make([]PluginUnit, 0, len(builtinPluginUnits)+1)
	for _, u := range builtinPluginUnits {
		units = append(units, u)
		for _, p := range u.Providers {
			inUnit[provKey(p.Class(), p.Reserved())] = true
		}
	}
	var rest []Provider
	for _, p := range r.allProviders() {
		if !inUnit[provKey(p.Class(), p.Reserved())] {
			rest = append(rest, p)
		}
	}
	if len(rest) > 0 {
		units = append(units, PluginUnit{Providers: rest})
	}
	return units
}

// allProviders returns every registered provider (sorted by key) — used by
// `charly __plugin serve` to expose the in-proc set over gRPC.
func (r *Registry) allProviders() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Provider, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// providersInPhase returns every registered provider whose lifecycle phase (F9) equals phase, in
// the stable registration order of allProviders. The kernel uses it to load/invoke plugins phase
// by phase — the bootstrap pre-pass enumerates PhaseBootstrap providers (compiled-in, registered
// at init) before config validation.
func (r *Registry) providersInPhase(phase string) []Provider {
	var out []Provider
	for _, p := range r.allProviders() {
		if phaseOfProvider(p) == phase {
			out = append(out, p)
		}
	}
	return out
}

// Close shuts down every connected plugin (each go-plugin client.Kill sends the
// gRPC Shutdown that stops the plugin server, then terminates the child — see
// plugin_transport.go's clientCloser). The host MUST run this on exit and on a
// shutdown signal, else the plugin servers orphan; main wires it as a deferred
// reap, an explicit post-dispatch reap, and a RegisterShutdownHook. Idempotent:
// closers are taken under the lock and nilled, so a second call is a no-op.
func (r *Registry) Close() error {
	r.mu.Lock()
	closers := r.closers
	r.closers = nil
	r.mu.Unlock()
	var firstErr error
	for _, c := range closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// primaryCarrier exposes a class:verb capability's declared scalar-sugar
// primary input field (ProvidedCapability.Primary), carried by both provider
// placements (in-proc + grpc) for placement parity.
type primaryCarrier interface{ primaryInput() string }
