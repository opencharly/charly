package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// builtinProviderInstances supplies every compiled-in Provider INSTANCE — verbs,
// kinds, deploy targets, steps, builders. The Go types can only be instantiated in
// Go (data cannot construct a Go value), but WHICH of them register is driven by the
// `providers:` manifest in the embedded charly.yml (parsed at init below), NOT by
// this list's membership: the manifest is the registry authority, this list supplies
// the instances it names, and init() gates the two into bijection. Adding a built-in
// is a charly.yml manifest entry PLUS its instance here; the gate fails loudly if
// either side is missing. (Replaces the five per-class init() registration loops.)
//
// EXCEPTION — the externalizable dedicated-provider pattern: a provider carrying NO served
// plugin schema may live in its OWN dedicated plugin_<class>_<name>.go file and self-register
// via registerDedicatedBuiltin, INTENTIONALLY absent from BOTH this slice and the `providers:`
// manifest, yet dispatching identically through providerRegistry. This covers a schema-LESS IR
// provider (a deploy-target / step / builder — derived from cross-refs or candy-internal, never
// user-authored, so no authored input to validate). NO KIND provider remains here: EVERY authoring
// kind is now an externalized plugin candy routed through runPluginKind — group (candy/plugin-group,
// C2-group), the 5 substrate kinds pod/vm/k8s/local/android (candy/plugin-substrate, C2-substrate),
// and the LAST one, the candy box⊻layer factory (candy/plugin-candy-kind, C2-candy). All are
// COMPILED-IN, host-decoding into the typed core maps (substrates → uf.Bundle/uf.Pod/uf.VM/…; candy
// → uf.Box/uf.Candy via the bootstrap-critical candyIsImage + buildCandy that STAY core) and
// validating their rich value host-side against the KEPT #<Kind>Value / #CandyValue def
// (validateKindValueCUE). So spec.KindWords is now EMPTY and checkKindProviderBijection over it is a
// no-op. See candy/plugin-deploy-local, plugin_step_external.go, candy/plugin-candy-kind.
var builtinProviderInstances = []Provider{
	// verbs (ClassVerb) — none of the extracted verbs are here: each is a dedicated plugin
	// UNIT that self-registers via RegisterBuiltinPluginUnit, absent from both this slice and
	// the providers: manifest. The goss-tier + state-provision verbs (process/port/dns/http/
	// interface/addr/matching/file/user/unix_group/kernel-param/mount/command/package/service)
	// are ALL relocated to compiled-in candies (candy/plugin-*), each registering the same way.
	// `command` is the install-task-act member and `package`/`service` are the TWO typed-step
	// members of that set (their step materialization stays in package main via materializeStep).
	// wl is NOT here either — it is an EXTERNAL-CHARLY-VERB served OUT-OF-PROCESS
	// (candy/plugin-wl), like dbus/record/cdp/vnc/mcp. wl was the LAST live-container verb
	// compiled into charly; after it externalized, ZERO check verbs are in-core and the
	// compiled-in live-verb seam was deleted (the live-verb externalization orphaned it).
	// `appium` (FIRST), `adb` (SECOND),
	// `kube` (THIRD), and `spice` (FOURTH) are the dep-shedders already EXTRACTED: each is
	// an external-charly-verb (candy/plugin-appium, candy/plugin-adb, candy/plugin-kube,
	// candy/plugin-spice, source github.com/…) served out-of-process — NOT a compiled-in
	// instance, absent from this slice AND the providers: manifest; its grpcProvider
	// registers at loadProjectPlugins time. NO dep-shedder remains here.
	pluginVerb{},
	// kinds (ClassKind) — NONE remain here, and NONE are dedicated-builtin KindProviders anymore:
	// EVERY authoring kind is an externalized plugin candy routed through runPluginKind. The tier-1
	// kinds (agent/module/sidecar/package-group/distro/builder/init/resource/target) + group
	// (candy/plugin-group, C2-group) + the 5 substrate kinds pod/vm/k8s/local/android
	// (candy/plugin-substrate, C2-substrate) + the LAST one, the candy box⊻layer factory
	// (candy/plugin-candy-kind, C2-candy — candyIsImage + buildCandy → uf.Box/uf.Candy, the
	// bootstrap-critical routing that STAYS core) are all COMPILED-IN plugins. So spec.KindWords is
	// EMPTY and checkKindProviderBijection over it is a no-op (every kind resolves via its ClassKind
	// provider — recognizedKind — not a #Node arm nor an in-proc KindProvider).
	// deploy targets (ClassDeployTarget) — ALL self-register from their dedicated
	// plugin_deploy_<name>.go files (the externalizable dedicated-provider pattern):
	// local, pod, vm, k8s, android.
	// steps (ClassStep) — the ONE remaining in-proc step provider self-registers from its dedicated
	// plugin_step_external.go file: ExternalPlugin. EVERY other builtin step kind's BUILD-emit
	// externalized to the compiled-in class:step plugin candy/plugin-installstep — NO in-proc
	// StepProvider, routed by pluginEmitStepWords: the PURE kinds (C1.1 file/shell-hook/shell-snippet/
	// service-packaged/service-custom/repo-change/apk-install + C1.6 reboot — apk-install & reboot are
	// no-op-emit, Emits=false) format their fragment directly from the step VIEW; the HOST-COUPLED
	// SystemPackages (C1.2) + Builder (C1.3) + LocalPkgInstall (C1.4) + Op (C1.5) OpEmit calls back the
	// host's "step-emit" host-builder. Their deploy leg stays sdk/kit.WalkPlans (reboot's is
	// the host-side guest reboot over RunHostStep → rebootVenueAndWait).
	// builders (ClassBuilder) — the four detection-builders (aur/pixi/cargo/npm) are EXTERNAL
	// out-of-process plugin candies (candy/plugin-builder-<word>): their build-time multi-stage
	// is resolved by the plugin's OpResolve leg (C10, kit.BuilderResolve, spliced by
	// emitBuilderStages), while their deploy-time IR shim
	// (per-candy stage context + teardown ops) is served over OpCollectContext/OpReverse and
	// resolved in the host-side build pre-pass (builder_preresolve.go). No in-proc BuilderProvider
	// remains; the registry resolves a builder word to its connected grpcProvider.
}

// registerDedicatedBuiltin self-registers a built-in Provider that lives in its OWN
// dedicated file (the externalizable dedicated-provider pattern): either a schema-less
// deploy-target / step / builder (no authored input), or a deploy-shape KIND validated by
// the closed CORE schema rather than a served plugin schema (plugin_group.go /
// plugin_substrate.go) — neither carries a `providers:`-manifest entry nor a
// builtinProviderInstances slice membership. Each such file calls this from a
// package-var initializer, which Go runs before ANY init() — so the per-class
// bijection gates in init() below observe the registration WITHOUT depending on
// cross-file init ordering (the alphabetical race the gates were structured to avoid).
// Returns true so the `var _ = registerDedicatedBuiltin(...)` call site (the codebase's
// `var _ = func() bool` registration idiom) reads cleanly. RegisterBuiltinProvider panics
// on a duplicate (class, word), so a provider left in BOTH the manifest/slice and a
// dedicated file is caught loudly at startup.
func registerDedicatedBuiltin(p Provider) bool { //nolint:unparam // bool result is the placeholder that keeps the pre-init `var _ = registerDedicatedBuiltin(...)` package-var registration idiom compiling; discarded by design, never consumed
	RegisterBuiltinProvider(p)
	return true
}

// providerManifest is the parsed `providers:` directive — provider class → the
// reserved words it contributes (matched against each instance's Class()+Reserved()).
type providerManifest map[string][]string

// builtinInstanceMap keys every compiled-in instance by provKey(Class, Reserved).
func builtinInstanceMap() map[string]Provider {
	m := make(map[string]Provider, len(builtinProviderInstances))
	for _, p := range builtinProviderInstances {
		m[provKey(p.Class(), p.Reserved())] = p
	}
	return m
}

// manifestInstanceProblems reports every break in the bijection between the manifest
// and the compiled-in instances: a manifest entry with no instance, or an instance no
// manifest entry names. Empty result ⇒ they agree exactly. Pure (no registration), so
// it is unit-testable with doctored inputs.
func manifestInstanceProblems(manifest providerManifest, byKey map[string]Provider) []string {
	var problems []string
	named := make(map[string]bool, len(byKey))
	for class, words := range manifest {
		for _, w := range words {
			key := provKey(ProviderClass(class), w)
			if _, ok := byKey[key]; !ok {
				problems = append(problems, key+" (manifest entry has no compiled-in instance)")
				continue
			}
			named[key] = true
		}
	}
	for key := range byKey {
		if !named[key] {
			problems = append(problems, key+" (compiled-in instance absent from the providers: manifest)")
		}
	}
	return problems
}

// unmarshalEmbeddedDefaults decodes the embedded charly.yml (the //go:embed default
// config) into dst via a minimal yaml decode — the shared reader for top-level directives
// (providers, context_ignore_baseline) that must be read WITHOUT the full node-form loader
// (the bootstrap circularity: the loader needs the kind providers the manifest registers).
// embeddedCharlyDefaults is populated before init(). Panics on an unparseable embed (a
// build-time invariant, never a runtime input).
func unmarshalEmbeddedDefaults(dst any) {
	if err := yaml.Unmarshal(embeddedCharlyDefaults, dst); err != nil {
		panic(fmt.Errorf("embedded charly.yml unparseable: %w", err))
	}
}

// parseEmbeddedProviderManifest extracts ONLY the `providers:` directive from the embedded
// charly.yml. embeddedCharlyDefaults is a //go:embed var, populated before init().
func parseEmbeddedProviderManifest() providerManifest {
	var doc struct {
		Providers providerManifest `yaml:"providers"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.Providers) == 0 {
		panic("registry bootstrap: embedded charly.yml has no providers: manifest")
	}
	return doc.Providers
}

// init is the ONE built-in provider registration site. The `providers:` manifest in
// the embedded charly.yml is the authoritative membership: it gates the manifest ⇄
// compiled-in instances into exact bijection, registers exactly the providers the
// manifest names, then runs every per-class bijection gate after ALL registration so
// each observes the full registry.
func init() {
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	if problems := manifestInstanceProblems(manifest, byKey); len(problems) > 0 {
		panic(fmt.Errorf("registry bootstrap: providers: manifest ⇄ compiled-in instances bijection broken: %v", problems))
	}
	for class, words := range manifest {
		for _, w := range words {
			RegisterBuiltinProvider(byKey[provKey(ProviderClass(class), w)])
		}
	}
	for _, gate := range []func() error{
		func() error { return checkVerbProviderBijection(spec.OpVerbs) },
		func() error { return checkKindProviderBijection(spec.KindWords) },
		checkDeployProviderBijection,
		checkStepProviderBijection,
	} {
		if err := gate(); err != nil {
			panic(err)
		}
	}
}
