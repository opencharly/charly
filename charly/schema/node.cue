// CUE schema for the UNIFIED `#Node` model — ONE name-first node shape for ALL of
// charly.yml: `<name>: {<kind>: <SCALARS>, <child-node>…}`.
//
// EVERYTHING IS A NODE. A kind's discriminator value holds only the entity's SCALAR
// fields; every NON-scalar field (composition, collection, single object) is a CHILD
// node `<name>-<key>: {<key>: <value>}`, every plan step is a CHILD step node, and a
// resource nested under a deployable kind is a sub-ENTITY child. The assembler
// (node_build.go) folds the data + step children back into the entity body and
// decodes it through the COMPLETE per-kind def (#Candy/#Deploy/…), so the strict
// field typing comes from the complete def — there are no per-arm value gaps.
//
// STRICTNESS: #Node is a DISJUNCTION of per-kind CLOSED arms (never matchN — that
// disables closedness). Each arm pins its one discriminator to a SCALAR-only kind
// value (#ForbidCollections forbids every non-scalar field in the value, forcing it
// to be a child) and narrows children to the legal set: data/step children
// (#ChildCommon) everywhere, plus resource arms (#ChildResource) under a deployable
// kind. A typo'd discriminator, an unknown value field, a wrong-kind child, and two
// discriminators all FAIL closedness.

// EVERY authoring kind is now EXTERNALIZED to a plugin — there are NO #Node arms left
// (C2-candy externalized the LAST one, `candy`, to candy/plugin-candy, after C2-group +
// C2-substrate). Each kind is recognized dynamically by the loader via a registered ClassKind
// provider (classifyDisc → providerRegistry.ResolveKind → recognizedKind), and its VALUE is
// validated HOST-SIDE against the KEPT core value defs (#CandyValue / #PodValue / #VmValue / …
// below) in runPluginKind (foldCandyKind / foldSubstrateKind) — a self-contained plugin schema
// cannot carry these rich core-referencing values (#Candy/#Box/#Deploy/#Vm/#LibvirtDomain/…),
// unlike group's small self-contained #GroupInput. The substrate + group kinds keep their
// #ResourceKind membership (so the loader still nests their members); candy is decoded by the
// BOOTSTRAP-CRITICAL core candyIsImage + buildCandy (which the discovered-candy pre-check calls
// directly — they stay core, so the COMPILED-IN plugin-candy has no bootstrap cycle). So #Node
// is now an OPEN struct (any node-shaped mapping) — the structural gate only; per-kind value
// closedness is the host-side gate. `_reservedNode` (the former child-name-shadow guard) is gone
// with the last arm — no core kind keyword remains for a child name to shadow.

// #ResourceKind — the DEPLOYABLE kinds: the kinds that nest a sub-ENTITY (resource)
// child (a deploy-into / alongside member), so a `<name>: {<kind>:…, <child>:
// {<resource-kind>:…}}` node is a bundle-shaped node. Every OTHER kind admits only
// data + step children. The loader (node_parse.go) classifies a resource child against
// this set; schemagen emits it as spec.ResourceKinds so the Go side derives the
// deployable vocabulary from this ONE CUE source instead of a hand list. (node.cue's
// per-arm child gate stays structural `_` — the deployable-vs-not check is the layered
// loader check; this enum is its single vocabulary source.)
//
// NOTE: NONE of these has a #Node arm anymore — group (C2-group), the 5 substrates
// (C2-substrate), and candy (C2-candy) are ALL plugin-served, so the arm-derived KindWords is
// now EMPTY. #ResourceKind is INDEPENDENT of KindWords: it is the set of kinds that NEST members
// (so the loader classifies a resource child + nests it), NOT the set with a #Node arm. Their
// members are pre-decoded host-side (buildResourceMemberChildren) and threaded to the plugin via
// op.Env (F5); the parser gate admits them because resourceKindSet has them. candy is NOT a
// resource kind (it nests no deploy members — it is the box⊻layer factory).
#ResourceKind: ("pod" | "vm" | "k8s" | "local" | "android" | "group") @go(-)

// ---------------------------------------------------------------------------
// Per-kind node VALUES — the COMPLETE per-kind def. A node's collections /
// composition / objects live in CHILD nodes, so they are ABSENT from the value
// (each def leaves them optional). The Go parser (node_parse.go) rejects any
// non-scalar field that appears in the value directly — the deterministic
// "everything is a node" gate — while CUE owns the closed-typo / unknown-field /
// wrong-kind-child strictness via the closed def + the child-narrowed arms.
// ---------------------------------------------------------------------------
// EDGE-INHERIT cutover D: `box:` merges INTO `candy:`. A `candy:` node is EITHER a
// LAYER fragment (#Candy, no base/from) OR a full IMAGE (#Image — a #Box that REQUIRES
// `base:` or `from:`, the former box:), routed by that marker in the loader
// (candyIsImage → uf.Box vs uf.Candy). The image arm REQUIRES base⊻from, and
// #Candy is the DEFAULT (`*`): an IMAGE carries base/from → bottoms #Candy (closed, no
// base) → resolves to #Image; a LAYER carries neither → #Image is incomplete (its base/
// from required-but-absent) while #Candy matches → the default resolves it to #Candy.
// That keeps the disjunction CONCRETELY validatable (a raw `#Candy | #Box` is NOT: a
// required-but-absent field is INCOMPLETE, not bottom, so a layer never eliminates the
// image arm). @go(-): the Go types are spec.Candy + spec.Box (decode picks by shape).
//
// C2-candy: candy has NO #Node arm anymore (externalized to candy/plugin-candy). #CandyValue is
// KEPT as the HOST-SIDE value gate: runPluginKind → foldCandyKind validates a candy node's value
// against #CandyValue (validateKindValueCUE) — the SAME closedness the #CandyArm gave — then runs
// candyIsImage + buildCandy (core, unchanged) and folds plugin-candy's echo into uf.Box/uf.Candy.
#Image:      #Box & ({base: string & !=""} | {from: string & !=""})
#CandyValue: (*#Candy | #Image) @go(-)
// EDGE-INHERIT cutover B: a substrate kind is BOTH the template entity AND the deploy
// (the eliminated `bundle:` role folds in). The value is the disjunction
// `#Template | #Deploy`, routed by SHAPE in the loader (a template carries its own
// config — source:/composition; a deploy carries from:/image: + the deploy config).
// The RDD spike proved `cue vet` resolves this disjunction unambiguously even with
// overlapping fields. @go(-): the Go types come from #Local/#Pod/#Vm/#K8s/#Android +
// #Deploy directly; this value def is validation-only.
//
// C2-substrate: these 5 substrate kinds have NO #Node arm anymore (externalized to
// candy/plugin-substrate, mirroring group). They are KEPT here as the HOST-SIDE value
// gate: runPluginKind validates a substrate node's authored value against #<Kind>Value
// (validateKindValueCUE) — the SAME closedness the #Node arm gave — because a
// self-contained plugin schema cannot carry these rich core-referencing values. So these
// defs stay REACHABLE from Go (via runPluginKind's kindValueDef lookup) while contributing
// NO #Node arm (KindWords drops the 5). #CandyValue above is the C2-candy analogue.
#LocalValue:   (#Local | #DeployValue) @go(-)
#PodValue:     (#Pod | #DeployValue) @go(-)
#VmValue:      (#Vm | #DeployValue) @go(-)
#K8sValue:     (#K8s | #DeployValue) @go(-)
#AndroidValue: (#Android | #DeployValue) @go(-)
// EVERY authoring kind is externalized to a plugin unit — the build-vocabulary kinds
// (`distro:`/`builder:`/`init:`/`resource:`), the Calamares `target:`/`package-group:`/`module:`,
// the AI-CLI grader `agent:`, the sidecar `sidecar:`, the targetless deploy `group:` (C2-group),
// the 5 substrate kinds `pod:`/`vm:`/`k8s:`/`local:`/`android:` (C2-substrate), AND the box⊻layer
// factory `candy:` (C2-candy) — so NONE has a #Node arm; such a node passes #NodeDoc as a
// registered non-core discriminator (the OPEN #Node struct). A plugin with a self-contained served
// #*Input schema (distro/builder/…/group) is validated by that schema (runPluginKind →
// validateAuthoredPluginInput); the substrates AND candy, whose value is rich + core-referencing
// (#Vm/#Deploy/#LibvirtDomain/#Candy/#Box/…) and so cannot be a self-contained plugin schema, are
// validated HOST-SIDE against the KEPT #<Kind>Value / #CandyValue defs above (runPluginKind →
// validateKindValueCUE). The core #Distro / #Builder / #Init / #Resource / #Target / #Agent /
// #Module / #Sidecar / #Pod / #Vm / #K8s / #Local / #Android / #Deploy / #Candy / #Box defs
// (schema/*.cue) are KEPT — they still generate spec.Distro / spec.Vm / spec.Candy / spec.Box /
// … (the canonical types the plugins' Invoke and the host decode into). For `group` the plugin
// (candy/plugin-group) decodes its scalar VALUE into the core spec.Deploy (#Deploy, kept via
// cue_kind_deploy.go) and attaches the host-threaded authored members; for the substrates
// candy/plugin-substrate ECHOES the host-pre-decoded canonical node (deploy BundleNode or
// per-substrate template), and candy/plugin-candy ECHOES the host-pre-decoded box⊻layer node
// (candyIsImage + buildCandy → spec.Box / spec.Candy) — the host folds into uf.Bundle /
// uf.Pod / uf.VM / … (substrate) and uf.Box / uf.Candy (candy).

// #DeployValue — the AUTHORED deploy shape (the disjunct under each substrate arm):
// the COMPLETE #Deploy minus the structural nested/peer maps + the derived target —
// all loader-derived from tree position, so authoring any of them is a closed-schema
// rejection (`run: charly migrate`). The substrate kind at the EDGE supplies the
// target; from:/image: supply the cross-ref (EDGE-INHERIT cutover B; was #BundleValue).
#DeployValue: #Deploy & {nested?: _|_, peer?: _|_, target?: _|_, member_of?: _|_, inside?: _|_}

// ---------------------------------------------------------------------------
// The unified node — an OPEN struct. C2-candy externalized the LAST #Node arm (`candy`,
// to candy/plugin-candy), after C2-group + C2-substrate. There are NO per-kind arms left:
// EVERY kind is plugin-provided (recognized via a registered ClassKind provider) and its
// VALUE is validated HOST-SIDE against the KEPT #CandyValue / #<Kind>Value defs above
// (runPluginKind → foldCandyKind / foldSubstrateKind → validateKindValueCUE). So #Node is
// the STRUCTURAL gate only: a node must be a mapping (a scalar entity value is rejected). The
// discriminator + per-child strictness is the LAYERED loader check (node_parse.go classifies
// each child + HARD-ERRORS a typo'd/absent/double discriminator and a wrong-kind child); the
// step-CHILD Op fields are typed by the VALIDATE entrypoint (charly box validate →
// validateNodeFormSteps, cue_schema.go). nodeDiscriminators (schemagen) derives an EMPTY
// KindWords from this arm-less #Node — every kind resolves via its ClassKind provider.
#Node: {...}

// #NodeDoc — a whole charly.yml document in unified node-form: the reserved DOCUMENT
// directives plus a flat map of name-first entity nodes. Validating a document
// against #NodeDoc is the load-time "validate-before-execute" gate.
#NodeDoc: close({
	version?:  #CalVer
	repo?:     string & !=""
	import?:   _
	discover?: _
	defaults?: _
	provides?: _
	// providers — the registry membership manifest (provider class → the words it
	// contributes). Read at init by registry_bootstrap.go to drive built-in provider
	// registration from the embedded charly.yml (the binary's default config);
	// recognized here so a document carrying it is not mis-read as a node named
	// "providers". A gate keeps it in bijection with the compiled-in instances.
	providers?: {[string]: [...string]}
	// compiled_plugins — the plugin CANDIES compiled into the charly binary (in-proc
	// placement). Read by the pluginsgen generator to emit charly/plugins_generated.go
	// (registerCompiledPlugin per entry) + the repo-root go.work; recognized here so a
	// document carrying it is not mis-read as a node named "compiled_plugins". A plugin
	// candy not listed still loads OUT-OF-PROCESS when referenced (the coexist path).
	compiled_plugins?: [...string]
	// context_ignore_baseline — the built-in build-context ignore patterns (VCS/binary
	// excludes + cache-hygiene globs), formerly the Go var baselineContextIgnore, now
	// data in the embedded charly.yml. Read by generate.go to emit .containerignore /
	// .dockerignore; a project's defaults.context_ignore still overlays on top.
	context_ignore_baseline?: [...string]
	// install_hints — binary name → (distro ID → package name) for `charly doctor` host
	// dependency install suggestions, formerly the Go var installHints (distro.go), now
	// data in the embedded charly.yml. An "AUR: <cmd>" value carries its own install line.
	install_hints?: {[string]: {[string]: string}}
	// ovmf_paths — distro family (fedora|arch|debian) → secure/nonsecure ordered OVMF
	// firmware candidate path pairs, formerly inline literals in ovmf_paths.go. The
	// alias→family resolution + secure selection + unknown-distro union stay Go logic;
	// only the path DATA is here. {code, vars} per candidate.
	ovmf_paths?: {[string]: {secure: [...{code: string, vars: string}], nonsecure: [...{code: string, vars: string}]}}
	// device_descriptions — host device path → human description for `charly doctor`'s
	// hardware section, formerly the Go var deviceDescriptions (doctor.go); data, not code.
	device_descriptions?: {[string]: string}
	// device_patterns — host device glob patterns probed for auto-detection
	// (DetectHostDevices) + `charly doctor`'s hardware section, formerly the Go var
	// devicePatterns (devices.go); data, not code.
	device_patterns?: [...string]
	// gpu_vendors — PCI vendor ID → name for the render nodes that count as a real,
	// encode-capable GPU (vs the paravirtual virtio-gpu), formerly the inline switch in
	// pickRenderNode (devices.go); key membership picks the DRINODE render node.
	gpu_vendors?: {[string]: string}
	// pci_class_labels — PCI class code (high 16 bits) → human label for VFIO passthrough
	// device reporting, formerly the inline switch in pciClassLabel (devices.go); an
	// unknown class falls back to the raw class (logic, not data).
	pci_class_labels?: {[string]: string}
	// distro_package_managers — host distro ID → install command prefix for `charly
	// doctor` install hints, formerly the inline switch in parseOsRelease (distro.go).
	distro_package_managers?: {[string]: string}
	// distro_family_map — host distro ID → base family for install-hint package-name
	// lookup, formerly the inline switch in distroFamily (distro.go); an unlisted distro
	// maps to itself (logic, not data).
	distro_family_map?: {[string]: string}
	// ovmf_distro_aliases — host distro ID → OVMF firmware family (fedora|arch|debian),
	// formerly the inline switches in ovmfCandidatesForDistro + ovmfNotFoundError
	// (ovmf_paths.go); an unlisted distro tries the union of all families (logic).
	ovmf_distro_aliases?: {[string]: string}
	{[!~"^(version|repo|import|discover|defaults|provides|providers|compiled_plugins|context_ignore_baseline|install_hints|ovmf_paths|device_descriptions|device_patterns|gpu_vendors|pci_class_labels|distro_package_managers|distro_family_map|ovmf_distro_aliases)$"]: #Node}
})
