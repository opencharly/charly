// Charly-name aliases for the generated `spec` types.
//
// WF-B repoints package main onto this package via zero-churn
// `type <CharlyName> = spec.<CharlyName>` aliases. For that to compile the spec
// package must EXPOSE every param type under its charly name. The DEFMAP names
// the (cue-def → charly) mapping.
//
// MECHANISM (RDD finding): `cue exp gengotypes` v0.16.1 does NOT propagate a
// def-level `@go(CharlyName)` rename to the FIELDS that reference that def — the
// referencing field keeps the original def name and the emitted Go dangles
// (uncompilable). Verified on a live spike. So the charly NAME is exposed here
// as a Go type alias (`type BoxConfig = Box`) instead of via a def-level
// attribute: identical drop-in semantics (the alias and the generated type are
// the SAME type), zero generated-file churn, and no broken references. The
// field-level `@go(GoName,…)` attributes (which DO work) carry the per-field
// name + pointer + type overrides in schema/*.cue.
//
// HAND-WRITTEN — not emitted by `task cue:gen`; the reproducibility gate
// (gen_repro_test.go) only covers cue_types_gen.go + vocab_gen.go.
package spec

// --- top-level entity types ---
type (
	BoxConfig      = Box
	CandyYAML      = Candy
	VmSpec         = Vm
	FleetNode      = Deploy
	LocalSpec      = Local
	PodSpec        = Pod
	KubernetesSpec = Kubernetes
	AndroidSpec    = Android
	SidecarDef     = Sidecar
	ResourceDef    = Resource
	DistroDef      = Distro
	BuilderDef     = Builder
	InitDef        = Init
)

// --- candy sub-types ---
type (
	ServiceEntry      = CandyService
	VolumeYAML        = CandyVolume
	AliasYAML         = CandyAlias
	ExtractYAML       = CandyExtract
	DataYAML          = CandyData
	MCPServerYAML     = CandyMCPProvide
	SecretYAML        = CandySecret
	HooksConfig       = CandyHook
	CandyCapabilities = CandyCapability
	RouteYAML         = CandyRoute
	VmSnapshotDecl    = VmSnapshot
)

// --- box / deploy sub-types ---
type (
	MergeConfig       = BoxMerge
	AliasConfig       = BoxAlias
	ReadinessConfig   = Readiness
	IterateConfig     = Iterate
	InstallOptsConfig = InstallOpts
	SecurityConfig    = Security
)

// --- wire sub-types renamed to dodge the schemagen kindValueDefs collision
// (a top-level `#<X>Value` CUE def auto-registers as a kind-value gate; see
// schema/init.cue's #EnvKV doc comment for the RDD-caught mechanism) ---
type (
	KeyValue = EnvKV
)
