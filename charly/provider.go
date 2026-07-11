package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opencharly/sdk"
)

// Provider is the ONE extension abstraction. Every reserved word — every kind,
// every verb, and the deploy-target / step / builder classes — is served by a
// Provider, which may be IN-PROCESS (a built-in, registered from init() wrapping
// today's handler funcs) or OUT-OF-PROCESS (a plugin, served over go-plugin gRPC).
// The registry, the bijection gate, and every call site treat both identically —
// the transport is invisible above the registry (see registry.go).
//
// The unified shape mirrors the CUE-single-source model: a reserved word maps to
// a handler taking CUE-generated params. `Invoke` is the wire-aligned form (it is
// exactly the proto Provider.Invoke envelope), so an out-of-proc plugin and an
// in-proc built-in are the same shape. Built-ins MAY additionally satisfy a typed
// fast-path adapter per class so an in-proc call skips JSON (added per class as
// the switch cutovers C1–C5 migrate each handler).
type Provider interface {
	// Reserved is the reserved word this provider serves ("exampleprobe","cdp",
	// "box","local","pixi"). It is the registry key within the provider's Class.
	Reserved() string
	// Class is which extension family the reserved word belongs to. A word may
	// exist in two classes (e.g. "k8s" is both a kind and a verb), so the registry
	// keys on (Class, Reserved), never Reserved alone.
	Class() ProviderClass
	// Invoke runs one operation. op.Op selects the operation for the class
	// (run/load/validate/emit/render/resolve); op.Params carries the CUE-typed
	// params; op.Env carries the serializable invocation context. The returned
	// Result.JSON is the class-appropriate payload (a CheckResult for a verb, an
	// InstallPlan for a deploy, Diagnostics for a kind validate, …).
	Invoke(ctx context.Context, op *Operation) (*Result, error)
}

// ProviderClass is the extension family a reserved word belongs to. A plugin's
// `provides:` lists capabilities as "<class>:<word>" (e.g. "verb:exampleprobe").
type ProviderClass string

const (
	ClassKind         ProviderClass = "kind"
	ClassVerb         ProviderClass = "verb"
	ClassDeployTarget ProviderClass = "deploy"
	ClassStep         ProviderClass = "step"
	ClassBuilder      ProviderClass = "builder"
	ClassCommand      ProviderClass = "command"
	// ClassBuild serves the BUILD-ENGINE DISPATCH words (build:box, build:generate): a
	// compiled-in plugin whose Invoke calls back the F10 HostBuild seam so the image-build /
	// generate engine runs host-side in-proc (the engine is I/O-bound with unexported state +
	// a huge blast radius, so it STAYS core — only a wire envelope crosses). See candy/plugin-build.
	ClassBuild ProviderClass = "build"
	// ClassLoader serves the unified-config PARSE (the single word "loader"): a compiled-in
	// plugin whose OpLoad parses the project (file read → import resolution → the #NodeDoc CUE
	// gate → reserved-word node decomposition → discover walk) into a generic, sdk-expressible
	// ParsedProject the host MATERIALIZES into the typed *UnifiedFile. Registered at init()
	// before the first LoadUnified call — no bootstrap cycle (the registry seed reads the
	// embedded providers: manifest via a plain yaml.Unmarshal, never LoadUnified). See
	// candy/plugin-loader (P6).
	ClassLoader ProviderClass = "loader"
	// ClassRefs serves the remote-repo FETCH BACKEND (the single word "refs"): a compiled-in
	// plugin implementing kit.RefsDownloader, whose Download turns a (repoPath, version) into a
	// populated local cache tree. The host dispatches every cache-miss remote-candy fetch through
	// the registered downloader, so an alternative refs plugin (OCI/S3) can swap the backend. The
	// host keeps the fetch orchestration (override/cache/migrate). See candy/plugin-refs (P7).
	ClassRefs ProviderClass = "refs"
)

// providerClasses is the closed set, used by the loader to validate a plugin's
// `provides:` entries and by the bijection gate.
var providerClasses = map[ProviderClass]bool{
	ClassKind: true, ClassVerb: true, ClassDeployTarget: true, ClassStep: true, ClassBuilder: true, ClassCommand: true, ClassBuild: true, ClassLoader: true, ClassRefs: true,
}

// splitCapability parses a "<class>:<word>" capability string as authored in a
// candy's `plugin.providers:` list. The wire form (proto ProvidedCapability) is
// already structured; this parses the YAML-authored declarations only.
func splitCapability(s string) (ProviderClass, string, bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	c := ProviderClass(s[:i])
	if !providerClasses[c] {
		return "", "", false
	}
	return c, s[i+1:], true
}

// Operation is the uniform invocation envelope (wire-aligned with proto
// InvokeRequest). Params and Env are JSON so the generated `spec` structs stay
// the single source of truth — there is no parallel proto type system (R3).
type Operation struct {
	Reserved string          `json:"reserved"` // the reserved word
	Op       string          `json:"op"`       // operation selector for the word's class
	Params   json.RawMessage `json:"params"`   // CUE-typed params (Op for verbs/steps; entity for kinds)
	Env      json.RawMessage `json:"env"`      // snapshotCheckEnv / venue descriptor
}

// Result is the uniform invocation result (wire-aligned with proto InvokeReply /
// Frame). JSON is the class-appropriate payload, decoded by the call site.
type Result struct {
	JSON json.RawMessage `json:"json"`
}

// PluginSchema is a plugin unit's CUE contract, obtained THROUGH the transport
// (gRPC Describe for an external, an in-proc handoff for a builtin) — never read
// from a candy's schema/ dir. CueSource is the package-less, SELF-CONTAINED .cue
// body the host splices onto its base schema (base ++ plugin) to validate authored
// plugin_input; InputDefs maps each provider key ("verb:externalprobe") to the CUE
// def that validates its plugin_input ("#ExternalprobeInput").
type PluginSchema struct {
	CueSource string
	InputDefs map[string]string
}

// PluginUnit is one self-contained plugin as the host sees it — its providers plus
// its schema. Built-in and external plugins both arrive as a *PluginUnit from
// PluginTransport.Connect, so the host code that loads, gates, and validates a
// plugin NEVER branches on which kind it is (the zero-distinction seam).
type PluginUnit struct {
	Providers []Provider
	Schema    PluginSchema
}

// Operation selectors (op.Op). Each class uses the subset it needs. Aliased to the SDK
// constants (sdk/ops.go) — the SINGLE SOURCE shared with compiled-in / out-of-tree plugin
// candies, so a kind candy's Invoke can compare req.GetOp() against the same value (R3).
const (
	OpRun      = sdk.OpRun
	OpLoad     = sdk.OpLoad
	OpValidate = sdk.OpValidate
	OpEmit     = sdk.OpEmit
	OpExecute  = sdk.OpExecute
	OpResolve  = sdk.OpResolve
	OpBuild    = sdk.OpBuild

	// Deploy-time builder-IR legs of an externalized detection-builder (cargo/npm/pixi/aur);
	// invoked host-side in the build PRE-PASS, never inside the pure BuildDeployPlan compile.
	OpCollectContext = sdk.OpCollectContext
	OpReverse        = sdk.OpReverse
)

// marshalParams / unmarshalResult are the small helpers the in-proc adapters and
// the gRPC stubs share so the envelope is built one way (R3).
func marshalJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
