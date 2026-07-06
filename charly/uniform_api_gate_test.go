package main

import (
	"reflect"
	"strings"
	"testing"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
	"google.golang.org/grpc"
)

// TestNoSinglePluginAPISurface is the F11 uniform-API gate — the architectural capstone of the
// externalization (the "generic over ad-hoc" invariant, R3). It asserts that NO provider WORD
// appears in the plugin↔kernel API SURFACE: not as an sdk.Op* selector VALUE, not as a
// ProvidedCapability / StepContract field NAME, not as a reverse-channel RPC METHOD name, and not
// as a hostBuilders registry KEY. Every such surface is a generic verb-of-action / class
// discriminator usable by ANY plugin of its class; the ONLY per-plugin data channel is the opaque
// json.RawMessage Substrate. A host subsystem MAY call the generic connectPluginByWord(class, word)
// / Provider.Invoke with a specific word ARGUMENT — the word is data, never API shape — so those
// call-sites are deliberately NOT scanned.
//
// The invariant is STRUCTURAL, not a user-count: each capability flag / Op is exercised by exactly
// ONE candy/plugin-example-* proof-plugin ("≥1 by construction"), so a naive "≥2 real users" rule
// would falsely fail. Genericity is proven by the word-free SHAPE of the surface, full stop.
//
// SCOPE NOTE: spec.Op carries only the GENERIC install-verb discriminators (mkdir/copy/write/… +
// the generic `plugin` dispatcher) plus shared modifiers — the 11 externalized live verbs
// (cdp/vnc/mcp/spice/libvirt/kube/adb/appium/wl/dbus/record) are NOT fields on spec.Op: each is
// served by its plugin's own `#*Input` def and desugared into the generic `plugin`/`plugin_input`
// pair before spec.Op validates. Those verb words enter the FORBIDDEN-WORD universe below via the
// compiled-in providers' Reserved() words (NOT spec.Op); the CUE slices spec.OpVerbs/OpFields/
// AuthoringVerbs/KindWords contribute the generic config vocabulary. This is a WORD SOURCE, not a
// SCANNED surface — the scanned surfaces are the SDK/RPC/registry API below.
func TestNoSinglePluginAPISurface(t *testing.T) {
	universe := buildProviderWordUniverse()
	if len(universe) == 0 {
		t.Fatal("provider-word universe is empty — the scan would be vacuous")
	}

	// scan returns every name that IS a provider word (lower-cased compare).
	scan := func(names []string) []string {
		var bad []string
		for _, n := range names {
			if universe[strings.ToLower(n)] {
				bad = append(bad, n)
			}
		}
		return bad
	}

	surfaces := []struct {
		name  string
		names []string
	}{
		{"sdk.Op* selector values", allOpSelectorValues()},
		{"sdk.ProvidedCapability field names", structFieldNames(reflect.TypeOf(sdk.ProvidedCapability{}))},
		{"sdk.StepContract field names", structFieldNames(reflect.TypeOf(sdk.StepContract{}))},
		{"ExecutorService RPC method names", serviceMethodNames(pb.ExecutorService_ServiceDesc)},
		{"CheckContextService RPC method names", serviceMethodNames(pb.CheckContextService_ServiceDesc)},
		{"Provider RPC method names", serviceMethodNames(pb.Provider_ServiceDesc)},
		{"PluginMeta RPC method names", serviceMethodNames(pb.PluginMeta_ServiceDesc)},
		{"hostBuilders kinds", hostBuilderKinds()},
	}
	for _, s := range surfaces {
		if bad := scan(s.names); len(bad) > 0 {
			t.Errorf("uniform-API violation: %s contains provider word(s) %v — that API surface must be class-generic (no per-plugin special-casing); carry per-plugin data in the opaque Substrate channel instead", s.name, bad)
		}
	}

	// Fixed method-set assertions: the reverse services must expose EXACTLY this allowlist. A new
	// RPC (even a generically-named one) must be added here CONSCIOUSLY — and if it were named after
	// a plugin, the word scan above would also catch it. This arm catches the "added a per-plugin
	// RPC" regression directly.
	// HostArbiter (C9) is a class-generic, action-multiplexed reverse-op class (the resource-arbiter
	// host-seam channel: config gather/resources + deploy running/stop/start + the GPU driver flip),
	// added CONSCIOUSLY like RunHostStep / InvokeProvider / HostBuild. Its NAME is not a provider word
	// (the word-scan above proves "hostarbiter" ∉ the universe), and its per-call detail is DATA (the
	// action string + spec params), never API shape — the F11 contract.
	assertMethodSet(t, "ExecutorService", pb.ExecutorService_ServiceDesc,
		"Venue", "RunSystem", "RunUser", "PutFile", "RunCapture", "GetFile", "RunHostStep", "InvokeProvider", "HostBuild", "HostArbiter")
	assertMethodSet(t, "CheckContextService", pb.CheckContextService_ServiceDesc,
		"HTTPDo", "AddBackground", "ResolveEndpoint")

	// Negative arm (teeth): a re-introduced provider word in a surface MUST be flagged. If this
	// scanner returned empty here, every positive arm above would be a false pass. spec.KindWords
	// is now EMPTY (C2-candy externalized the LAST built-in kind arm), so probe with ANY word from
	// the universe (OpVerbs / compiled-in provider words are always present) instead of KindWords[0].
	probe := ""
	for w := range universe {
		probe = w
		break
	}
	if probe == "" {
		t.Fatal("provider-word universe is empty — the teeth arm has no word to probe")
	}
	if bad := scan([]string{"GenericAction", probe}); len(bad) != 1 || bad[0] != probe {
		t.Fatalf("word-free scanner has no teeth: scanning a surface containing the provider word %q returned %v, want exactly [%q]", probe, bad, probe)
	}
}

// genericConceptCollisions are words that appear in the authored-config vocabulary AND legitimately
// name a GENERIC reverse-channel API element (a shared deploy concept, not a plugin identity), so
// they are excluded from the forbidden-word universe. Currently only "venue": the generic
// ExecutorService.Venue RPC (return the venue identifier) coincides with the #Op `venue` config
// field. Extend ONLY with a justification — never to silence a real per-plugin leak.
var genericConceptCollisions = map[string]bool{"venue": true}

// buildProviderWordUniverse is the set of words that must NOT appear in the plugin↔kernel API
// surface — the UNION of every CUE-derived authored-config word slice (spec.OpVerbs act-verbs ∪
// spec.KindWords ∪ spec.OpFields ∪ spec.AuthoringVerbs — the GENERIC config vocabulary) PLUS every
// compiled-in NON-command provider's Reserved() word, which is where the externalized check verbs
// (cdp/vnc/mcp/kube/libvirt/spice/adb/appium/…) enter, since they are plugin-served and NOT spec.Op
// fields — MINUS genericConceptCollisions. This SUBSUMES "no provider word" (every plugin word is
// one of these CUE-derived names or a registered provider word) and additionally keeps the API
// surface's names disjoint from the config vocabulary. The drift-guarded CUE slices + the live
// registry keep it current automatically.
//
// Command words (status/start/stop/shell/logs) are excluded: they are generic English verbs that
// COINCIDE with sdk.Op* lifecycle selector VALUES (OpStatus="status", …), and a command word
// leaking into the invocation API is not a real failure mode (a command has only OpRun).
func buildProviderWordUniverse() map[string]bool {
	u := map[string]bool{}
	add := func(words []string) {
		for _, w := range words {
			if lw := strings.ToLower(w); !genericConceptCollisions[lw] {
				u[lw] = true
			}
		}
	}
	add(spec.OpVerbs)
	add(spec.KindWords)
	add(spec.OpFields)
	add(spec.AuthoringVerbs)
	for _, p := range providerRegistry.allProviders() {
		if p.Class() == ClassCommand {
			continue
		}
		if lw := strings.ToLower(p.Reserved()); !genericConceptCollisions[lw] {
			u[lw] = true
		}
	}
	return u
}

// allOpSelectorValues is the full sdk.Op* selector set (the generic verb-of-action vocabulary). A
// new Op MUST be added here so the gate scans it.
func allOpSelectorValues() []string {
	return []string{
		sdk.OpRun, sdk.OpLoad, sdk.OpValidate, sdk.OpEmit, sdk.OpExecute, sdk.OpResolve,
		sdk.OpCollectContext, sdk.OpReverse,
		sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor, sdk.OpPostTeardown,
		sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell, sdk.OpRebuild,
		sdk.OpPreresolve, sdk.OpBootstrap,
	}
}

// structFieldNames returns the exported field names of a struct type.
func structFieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// serviceMethodNames returns every RPC method name of a gRPC service (unary Methods + streaming Streams).
func serviceMethodNames(desc grpc.ServiceDesc) []string {
	var out []string
	for _, m := range desc.Methods {
		out = append(out, m.MethodName)
	}
	for _, s := range desc.Streams {
		out = append(out, s.StreamName)
	}
	return out
}

// hostBuilderKinds returns the registered HostBuild kinds (F10).
func hostBuilderKinds() []string {
	var out []string
	for k := range hostBuilders {
		out = append(out, k)
	}
	return out
}

// assertMethodSet asserts a service exposes EXACTLY the given method names (order-independent).
func assertMethodSet(t *testing.T, service string, desc grpc.ServiceDesc, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, m := range serviceMethodNames(desc) {
		got[m] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
		if !got[w] {
			t.Errorf("%s: expected RPC method %q is missing", service, w)
		}
	}
	for m := range got {
		if !wantSet[m] {
			t.Errorf("%s: unexpected RPC method %q — a new reverse RPC must be added to the F11 allowlist CONSCIOUSLY (and must be class-generic, never named after one plugin)", service, m)
		}
	}
}
