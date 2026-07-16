package main

// plugin_prescan.go — the EXTERNAL-deploy-substrate parse pre-scan.
//
// An external (out-of-tree) deploy provider's reserved word (e.g. `exampledeploy`)
// is only registered in providerRegistry AFTER loadProjectPlugins builds + connects
// the plugin — which happens only AFTER LoadConfig parses the project charly.yml.
// But a `disposable: true` check bed (or any deploy) may use that word as its
// SUBSTRATE discriminator (`check-foo: { exampledeploy: {…} }`), which the node-form
// parser must recognize at LOAD time, before the provider connects. Chicken-and-egg.
//
// The fix (the lightweight declaration pre-scan): before the root entity nodes are
// normalized (the walk's Boundary seam — loaderkit.Walk calls this at the root file AND
// each namespace root, before that boundary's documents parse), cheaply read each
// discovered candy's `plugin:` DECLARATION and register the DEPLOY words it declares
// as recognized substrate discriminators. This needs only the WORD + its class — no
// provider instance, no build, no connect (the real provider still connects later at
// loadProjectPlugins and dispatches the actual Add). The parse hooks
// (classifyDisc / isResourceDisc / normalizeNodeInto / validateCheckBeds) consult
// recognizedDeploySubstrate, which is satisfied by EITHER a connected provider OR a
// pre-scanned declaration.
//
// The scan is purely ADDITIVE and best-effort: it only widens the set of recognized
// substrate words and never changes any existing classification, so a project with
// no external deploy substrate is unaffected (the byte-gate skips every non-plugin
// candy before any YAML parse).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
)

// declaredDeploySubstrates holds the external DEPLOY-provider words a project's
// candy `plugin:` declarations name, learned by the pre-scan before the provider
// connects. Process-wide + additive: recognizing a superset of substrate words is
// harmless (an unconnected word that reaches dispatch fails loudly at ResolveDeploy),
// and the common no-external-substrate project never adds an entry.
var (
	declaredDeployMu        sync.RWMutex
	declaredDeploySubstrate = map[string]bool{}
	// declaredExternalVerb holds the external (out-of-tree) VERB words a project's candy
	// plugin declarations name — learned POST-SCAN by registerExternalVerbsFromCandies
	// (called at Validate), NOT by the parse-time prescan: a @github-composed plugin candy
	// is fetched DURING the scan, after the prescan, so only the scanned candy map sees its
	// `verb:<word>` declaration. Lets a build-context `run:` plugin verb step validate as
	// build-emit-capable WITHOUT building+connecting the plugin (standalone `charly box
	// validate`). Additive, best-effort, no-false-negatives: an over-broad recognition is
	// harmless — a verb that turns out non-build-emit-capable fails loudly at build via
	// emitPluginFragment's empty-fragment guard. Shares declaredDeployMu (the one lock).
	declaredExternalVerb = map[string]bool{}
	// declaredExternalStep holds the external (out-of-tree) STEP words a project's candy plugin
	// declarations name — learned POST-SCAN alongside the verbs (registerExternalVerbsFromCandies),
	// so a `run: plugin: <step-word>` step (a class:step plugin — F3's external step KIND) validates
	// as a DEPLOY-capable act in standalone `charly box validate` WITHOUT connecting the plugin
	// (compileActOp lowers it to an externalStep at deploy). Same additive/best-effort contract as
	// declaredExternalVerb; shares declaredDeployMu.
	declaredExternalStep = map[string]bool{}
	// declaredExternalCommand holds the external (out-of-tree) COMMAND words a project's candy
	// plugin declarations name — learned by the SAME byte-gated prescan, but consumed EARLY
	// (in main, before kong.Parse) so an external command plugin's CLI word reaches the Kong
	// grammar before the provider is connected. The connect is LAZY: only when the user
	// actually invokes `charly <word>` does dispatchExternalCommand build+connect the plugin
	// (collectExternalCommandPlugins builds a grammar holder from the prescanned word; the
	// dispatch entry carries the word and lazy-connects). Additive/best-effort: a project
	// with no command plugins registers nothing, so the grammar is byte-for-byte unchanged.
	// Shares declaredDeployMu (the one lock).
	declaredExternalCommand = map[string]bool{}
	// declaredKind holds the external (out-of-tree) KIND words a project's candy plugin
	// declarations name — learned by the SAME byte-gated prescan (F4). It lets the loader
	// RECOGNIZE a `kind: <plugin-word>` discriminator at PARSE time (classifyDisc) before the
	// serving plugin connects — the kind analogue of declaredDeploySubstrate. The serving
	// plugin is connected by a depth-0 pre-pass (connectDeclaredKindPlugins) so
	// normalizeNodeInto's runPluginKind can decode the body. Shares declaredDeployMu (the one
	// lock); empty for a project with no external kind plugins.
	declaredKind = map[string]bool{}
)

// recognizedKind reports whether word names a kind the loader may treat as an entity
// discriminator: EITHER a connected kind provider (built-in / compiled-in / already-loaded
// external) OR a pre-scanned external declaration (F4). The kind analogue of
// recognizedDeploySubstrate (R3) — used by classifyDisc + normalizeNodeInto so a
// declared-but-not-yet-connected external kind classifies + decodes.
func recognizedKind(word string) bool {
	if _, ok := providerRegistry.ResolveKind(word); ok {
		return true
	}
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredKind[word]
}

// recognizedStructuralKind reports whether `word` resolves to a CONNECTED provider that decodes a
// STRUCTURAL entity (F5) — a plugin kind whose OpLoad reply is a spec.Deploy member tree the host
// folds into uf.Bundle. Precisely EXCLUDES FLAT plugin kinds and the tier-1 kinds (distro/builder/
// init/target/agent/module/sidecar/package-group), which are registered providers but NOT structural.
func recognizedStructuralKind(word string) bool {
	prov, ok := providerRegistry.ResolveKind(word)
	if !ok {
		return false
	}
	sc, ok := prov.(structuralKindCarrier)
	return ok && sc.isStructuralKind()
}

// isDeclaredExternalKind reports whether `word` is a pre-scan-DECLARED external plugin kind (an F4/F5
// plugin candy's `kind:<word>` declaration) whose out-of-process provider may not be connected yet.
// This set is external-only — a core kind is never declared via a plugin manifest.
func isDeclaredExternalKind(word string) bool {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredKind[word]
}

// externalKindMayNestMembers reports whether a node whose discriminator is `word` may nest
// sub-ENTITY (resource-member) children at PARSE time because `word` is an EXTERNAL STRUCTURAL plugin
// kind (F5 authored-member input-threading). It admits ONLY a connected STRUCTURAL kind — a FLAT
// plugin kind and every non-structural CORE kind (candy/distro/…) stay guarded (the loader parse's
// resourceKindSet check covers the core DEPLOY kinds). During the depth-0 connect pre-pass a declared
// external kind may not be connected yet (structural-ness unknown), so it is admitted THERE and the
// definitive decision is deferred to runPluginKind once connected: a STRUCTURAL kind reconstructs the
// authored members; a FLAT kind hard-errors on member children (never a silent drop). Mirrors
// recognizedDeploySubstrate's "declared-before-connected" leniency.
func externalKindMayNestMembers(word string) bool {
	if recognizedStructuralKind(word) {
		return true
	}
	// A DECLARED external kind may nest members whether or not its provider is connected
	// yet — inside the connect pre-pass (provider not built), AND outside it when the
	// provider FAILED to build/connect (a degraded environment: a fetched remote repo
	// loaded in a minimal container with no Go toolchain). Both cases defer the
	// member-nesting decision to normalizeNodeInto, which decodes via the connected
	// provider or gracefully warn-skips when it never connected. Gating this on
	// inKindConnectPass would hard-error the member-nesting parse (node_parse.go) before
	// the normalizer's graceful skip could run — the box.list.boxes-on-a-fetched-repo
	// failure (a structural `examplestructkind` bed with a `local` member, plugin unbuilt).
	return isDeclaredExternalKind(word)
}

// registerDeclaredKind records one declared external kind word (F4).
func registerDeclaredKind(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredKind[word] = true
	declaredDeployMu.Unlock()
}

// declaredKindWords returns a snapshot of the pre-scanned external kind words — the set the
// depth-0 connect pre-pass (connectDeclaredKindPlugins) builds + connects so a flat external
// kind body decodes via runPluginKind. A copy under the shared lock.
func declaredKindWords() []string {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	out := make([]string, 0, len(declaredKind))
	for w := range declaredKind {
		out = append(out, w)
	}
	return out
}

// inKindConnectPassFlag guards connectDeclaredKindPlugins against re-entrancy: connecting an
// external kind plugin must load the project (LoadConfig / ScanAllCandy → LoadUnified), which
// re-loads the SAME root that CONTAINS the `kind: <plugin-word>` node. When set, the nested load's
// connect pre-pass is a no-op AND normalizeNodeInto DEFERS (skips) an unconnected kind node, so the
// nested load succeeds and the OUTER pass then has the providers registered. The loader is
// single-threaded per load; the flag rides declaredDeployMu for safety.
var inKindConnectPassFlag bool

// declaredKindConnectErr retains the actual host build/connect failure for a
// declared external kind. The normalizer still permits read-only discovery of
// unrelated nodes, but its mandatory warning must carry the actionable cause.
var declaredKindConnectErr = map[string]error{}

func inKindConnectPass() bool {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return inKindConnectPassFlag
}

func setKindConnectPass(v bool) {
	declaredDeployMu.Lock()
	inKindConnectPassFlag = v
	declaredDeployMu.Unlock()
}

func declaredKindConnectError(word string) error {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredKindConnectErr[word]
}

func recordDeclaredKindConnectError(need map[string]struct{}, err error) {
	declaredDeployMu.Lock()
	defer declaredDeployMu.Unlock()
	for word := range need {
		declaredKindConnectErr[word] = err
	}
}

// finalizeDeclaredKindConnections clears a retained cause only after the exact
// declared word has registered. A later namespace boundary can legitimately
// scan no matching plugin candy and make loadProjectPlugins return nil; that is
// not a successful connection and must never erase the earlier causal error.
func finalizeDeclaredKindConnections(need map[string]struct{}) {
	declaredDeployMu.Lock()
	defer declaredDeployMu.Unlock()
	for word := range need {
		if _, connected := providerRegistry.ResolveKind(word); connected {
			delete(declaredKindConnectErr, word)
			continue
		}
		if _, recorded := declaredKindConnectErr[word]; !recorded {
			declaredKindConnectErr[word] = fmt.Errorf("declared kind provider %q did not register during load", word)
		}
	}
}

// connectDeclaredKindPlugins host-builds + connects the out-of-process plugins serving the
// project's declared external KIND words (F4), so a `kind: <plugin-word>` entity decodes via
// runPluginKind during load. Called at the walk's Boundary seam AFTER the prescan and BEFORE the
// host materialize (materializeLoadedProject, loader_driver.go) decodes/folds the entity nodes.
// The connect re-loads the project (LoadConfig +
// ScanAllCandyWithConfigOpts → LoadUnified, which fetches @github kind candies too), so it is
// GUARDED by inKindConnectPass — the nested load skips this pre-pass and DEFERS its kind nodes
// (normalizeNodeInto), so the scan succeeds; this OUTER pass then has the providers registered.
// Best-effort + idempotent: a project with no external kind plugins (or one whose kinds are
// already connected — compiled-in / prior-loaded) does zero work; a connect FAILURE leaves the
// kind unconnected, and normalizeNodeInto then WARN-SKIPS its nodes (a loud stderr warning + a
// drop, never a silent drop nor a hard load error — so read-only commands still work in a
// degraded environment; a command that USES the kind fails loudly at that point).
func connectDeclaredKindPlugins(dir string) {
	if inKindConnectPass() {
		return // nested load inside an outer connect — the outer pass owns the connect
	}
	need := map[string]struct{}{}
	for _, w := range declaredKindWords() {
		if _, ok := providerRegistry.ResolveKind(w); !ok {
			need[w] = struct{}{}
		}
	}
	if len(need) == 0 {
		return
	}
	setKindConnectPass(true)
	defer setKindConnectPass(false)
	cfg, err := LoadConfig(dir)
	if err != nil {
		recordDeclaredKindConnectError(need, fmt.Errorf("load project configuration: %w", err))
		return // config load failure → kinds stay unconnected → normalizeNodeInto warn-skips them
	}
	candyMap, err := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{})
	if err != nil {
		recordDeclaredKindConnectError(need, fmt.Errorf("scan project candies: %w", err))
		return
	}
	if err := loadProjectPlugins(context.Background(), candyMap, need); err != nil {
		recordDeclaredKindConnectError(need, err)
		return
	}
	finalizeDeclaredKindConnections(need)
}

// recognizedDeploySubstrate reports whether word names a deploy substrate the
// loader may treat as an entity discriminator: EITHER a connected deploy provider
// (built-in or already-loaded external) OR a pre-scanned declaration. The single
// predicate the four parse hooks share (R3).
func recognizedDeploySubstrate(word string) bool {
	if _, ok := providerRegistry.ResolveDeploy(word); ok {
		return true
	}
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredDeploySubstrate[word]
}

// isExternalDeploySubstrate reports whether target names an EXTERNAL (out-of-process)
// deploy substrate served over the E3b reverse channel. Two cases:
//
//   - A NON-kind word (e.g. exampledeploy, not in resourceKindSet) is external iff
//     recognized — a connected provider OR a pre-scanned declaration.
//   - A CUE-kind substrate word (pod/vm/k8s/local/android/group ∈ resourceKindSet) is
//     external iff it has been MIGRATED to an external plugin (externalizedDeploySubstrates,
//     F1) AND a plugin declaring it is recognized. A still-builtin substrate kind
//     (pod/vm/k8s/local/group today) is NOT external — its in-proc DeployTargetProvider
//     serves it. (A group's Target is "" and never matches.)
//
// A true result makes the bed runner treat the deploy like a kind:local deploy (no
// image build, no config/start, bundle-del teardown). This classification is keyed on
// the ROOT deploy node: the android R10 bed is a POD root with NESTED target:android
// children — its pod root is NOT externalized (returns false → normal image-build +
// charly start), while each nested android child resolves to the external plugin
// through its own per-child ResolveTarget dispatch, not this root classifier.
func isExternalDeploySubstrate(target string) bool {
	if target == "" {
		return false
	}
	if resourceKindSet[target] {
		return externalizedDeploySubstrates[target] && recognizedDeploySubstrate(target)
	}
	return recognizedDeploySubstrate(target)
}

// registerDeclaredDeploySubstrate records one declared external deploy word.
func registerDeclaredDeploySubstrate(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredDeploySubstrate[word] = true
	declaredDeployMu.Unlock()
}

// isDeclaredExternalVerb reports whether word was registered as an external verb a
// plugin candy declares (registerExternalVerbsFromCandies, post-scan) — used by
// opActsInBuildDeploy ONLY when the verb does NOT resolve in the registry (standalone
// `charly box validate`, where external plugins are not connected). A CONNECTED verb
// (builtin always, or an external connected via the build-time connect seam) is
// classified by its provider type, not this map.
func isDeclaredExternalVerb(word string) bool {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredExternalVerb[word]
}

// registerDeclaredExternalVerb records one declared external verb word.
func registerDeclaredExternalVerb(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredExternalVerb[word] = true
	declaredDeployMu.Unlock()
}

// isDeclaredExternalStep reports whether word was registered as an external STEP a plugin candy
// declares — used by opActsInBuildDeploy when the step word does NOT resolve in the registry
// (standalone `charly box validate`, plugins not connected). A `run: plugin: <step-word>` lowers
// to an externalStep at deploy (F3), so it is a real DEPLOY act.
func isDeclaredExternalStep(word string) bool {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredExternalStep[word]
}

// registerDeclaredExternalStep records one declared external step word.
func registerDeclaredExternalStep(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredExternalStep[word] = true
	declaredDeployMu.Unlock()
}

// registerDeclaredExternalCommand records one declared external command word.
func registerDeclaredExternalCommand(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredExternalCommand[word] = true
	declaredDeployMu.Unlock()
}

// declaredExternalCommandWords returns a snapshot of the prescanned external command
// words — the grammar holders collectExternalCommandPlugins builds before any provider
// is connected, so `charly <word>` parses; the connect is deferred to dispatch.
func declaredExternalCommandWords() []string {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	words := make([]string, 0, len(declaredExternalCommand))
	for w := range declaredExternalCommand {
		words = append(words, w)
	}
	return words
}

// registerExternalVerbsFromCandies registers the external (out-of-tree) VERB and STEP words every
// scanned plugin candy declares, so a `run:` plugin verb/step validates as build/deploy-act-capable
// in standalone `charly box validate` (where the provider is not connected): a verb is build-emit-
// capable, a class:step lowers to an externalStep at deploy (F3). It runs over the SCANNED candy map — which includes @github-composed plugin
// candies fetched DURING the scan — so it recognizes a verb whether the plugin candy is
// locally vendored OR pulled via @github (the gap the parse-time prescan, which sees only
// locally-discovered dirs, cannot close). Builtins are skipped: they register their verbs
// at init(), so ResolveVerb already classifies them (this map is the not-connected path).
func registerExternalVerbsFromCandies(candies map[string]*Candy) {
	for _, candy := range candies {
		if candy == nil || candy.Plugin == nil {
			continue
		}
		if src := candy.Plugin.Source; src == "" || src == "builtin" {
			continue
		}
		for _, capability := range candy.Plugin.Providers {
			if class, word, ok := splitCapability(string(capability)); ok {
				switch class {
				case ClassVerb:
					registerDeclaredExternalVerb(word)
				case ClassStep:
					registerDeclaredExternalStep(word)
				}
			}
		}
	}
}

// prescanDeclaredPluginWords reads the project root's `discover:` directive from
// rootData (leniently — ignoring everything else), walks each discovered manifest
// dir, and registers every external DEPLOY word a candy's `plugin: providers:`
// declares. baseDir anchors relative discover paths. Best-effort: any I/O or parse
// error on a single file is skipped, never fatal — a malformed candy must not break
// `charly status` / `charly box validate` repo-wide.
func prescanDeclaredPluginWords(rootData []byte, baseDir string) {
	var doc struct {
		Discover kit.DiscoverConfig `yaml:"discover"`
	}
	if err := yaml.Unmarshal(rootData, &doc); err != nil || len(doc.Discover) == 0 {
		return
	}
	for _, spec := range doc.Discover {
		if spec.Path == "" {
			continue
		}
		manifest := spec.Manifest
		if manifest == "" {
			manifest = UnifiedFileName
		}
		root := spec.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(baseDir, root)
		}
		dirs, err := kit.FindEntityDirs(root, manifest, spec.Recursive)
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			prescanPluginManifest(filepath.Join(dir, manifest))
		}
	}
}

// prescanPluginManifest reads one candy manifest's `plugin: providers:` declarations
// and registers each external DEPLOY substrate word (the PARSE-time loader recognition
// that lets a root-charly.yml bed using an external deploy substrate as its entity
// discriminator LOAD before the provider connects) AND each external COMMAND word (the
// CLI-grammar recognition that lets `charly <word>` parse before the provider connects —
// consumed EARLY by prescanProjectCommandWords in main, ahead of kong.Parse). External
// VERB words are NOT registered here: they are needed only at Validate (opActsInBuildDeploy),
// and a @github-composed plugin candy is fetched only DURING the scan (after this parse-time
// prescan), so verbs register post-scan from the candy map (registerExternalVerbsFromCandies).
// The byte-gate skips every non-plugin manifest before paying for a YAML parse. Best-effort.
func prescanPluginManifest(path string) {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("plugin:")) {
		return
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return
	}
	var providers []string
	collectPluginProviders(root, &providers)
	collectPluginPrimaries(root)
	for _, p := range providers {
		class, word, ok := splitCapability(p)
		if !ok {
			continue
		}
		switch class {
		case ClassDeployTarget:
			registerDeclaredDeploySubstrate(word)
		case ClassCommand:
			registerDeclaredExternalCommand(word)
		case ClassKind:
			registerDeclaredKind(word)
		}
	}
}

// collectPluginPrimaries recursively registers every `plugin: { primary: {word:
// field} }` scalar-sugar declaration in a decoded manifest — the byte-gated
// prescan twin of the served ProvidedCapability.Primary, needed BEFORE the
// out-of-process provider connects (the parse-time desugar runs pre-connect).
func collectPluginPrimaries(v any) {
	switch t := v.(type) {
	case map[string]any:
		if pv, ok := t["plugin"].(map[string]any); ok {
			if prim, ok := pv["primary"].(map[string]any); ok {
				for word, field := range prim {
					if f, ok := field.(string); ok && f != "" {
						// best-effort: a collision registers nothing (the provider
						// registration gate reports it loudly later)
						_ = registerPluginPrimary(word, f)
					}
				}
			}
		}
		for _, cv := range t {
			collectPluginPrimaries(cv)
		}
	case []any:
		for _, cv := range t {
			collectPluginPrimaries(cv)
		}
	}
}

// collectPluginProviders recursively gathers every `plugin: { providers: [...] }`
// capability string in a decoded node-form manifest (the plugin block sits on a
// `<name>-decl:` CHILD node, so a generic descent finds it regardless of nesting).
func collectPluginProviders(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if pv, ok := t["plugin"].(map[string]any); ok {
			if provs, ok := pv["providers"].([]any); ok {
				for _, p := range provs {
					if s, ok := p.(string); ok {
						*out = append(*out, s)
					}
				}
			}
		}
		for _, child := range t {
			collectPluginProviders(child, out)
		}
	case []any:
		for _, e := range t {
			collectPluginProviders(e, out)
		}
	}
}
