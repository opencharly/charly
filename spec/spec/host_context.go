package spec

// host_context.go — the deploy-compile HOST CONTEXT value type, promoted from sdk/deploykit
// (#55 K4 import-purity). A hand-written spec value carrier, NOT a CUE wire type: it crosses the
// plugin boundary ONLY as the opaque `host_context: bytes` RawBody payload on
// #DeployCompileRequest (the sanctioned VmJSON/PodConfigJSON idiom), so CUE never decodes it at the
// wire. Its WIRE form is the 4 host-computed SCALARS below (MachineVenue/Distro/GlibcVersion/
// BuilderImage); the remaining fields (BuilderContext/ActiveInitName/ActiveInit) are `json:"-"`
// IN-PROCESS-ONLY — populated PLUGIN-SIDE after the HostContextJSON decode (candy/plugin-fleet's
// preresolveBuilderContexts over the reverse channel + the rp.Init active-init resolve), never
// crossing. The `cue exp gengotypes` spike is the SDD arbiter: it CANNOT express the `json:"-"`
// intent (it emits `,omitempty`, which would wrongly serialize the in-process fields into the wire
// blob) and it embeds the already-hand spec.BuilderPreresolved (opaque map[string]any Context) — so
// hand-written is SPIKE-JUSTIFIED per the SDD mandate, mirroring its child BuilderPreresolved.
// sdk/deploykit keeps a `type HostContext = spec.HostContext` forwarder so the deploy-compile
// mechanism (BuildDeployPlan + the compile helpers) + candy/plugin-fleet compile unchanged.
type HostContext struct {
	// MachineVenue selects compilation mode (P9): false (zero value) → compile for a
	// CONTAINER image build (the pod overlay / OCI target); true → compile for a MACHINE
	// venue with a system init (a target:local / target:vm deploy — services render as
	// systemd units, home is deferred via {{.Home}}). detectHostContext sets it true for a
	// host deploy; the OCI/pod-overlay compile passes the zero value. It replaced the former
	// string Target ("host"/"vm"/"oci"), whose "vm" arm was dead (a vm deploy compiles with
	// the host detectHostContext) — the machine-vs-container distinction IS the trait.
	MachineVenue bool

	// Distro is the resolved host distro tag, e.g. "fedora:43". Used to
	// pick the right format section when compiling for a host target
	// whose distro differs from the image's primary distro.
	Distro string

	// GlibcVersion is the host's glibc major.minor as reported by
	// `ldd --version`. Used by the host target's preflight check against
	// the selected builder image. Optional; empty means skip the check.
	GlibcVersion string

	// BuilderImage overrides the default builder-image selection for
	// VenueContainerBuilder steps. Populated from --builder-image. ""
	// means "use the embedded build vocabulary's default".
	BuilderImage string

	// BuilderContext carries the host-side build PRE-PASS result: each externalized
	// detection-builder's per-candy stage context + teardown ops, keyed by
	// BuilderCtxKey(candy, builder). Populated by preresolveBuilderContexts BEFORE
	// this pure compile (the deploy command path); read by collectBuilderContext +
	// compileBuilderSteps so the compiler NEVER dials a builder plugin (purity). Nil
	// when no pre-pass ran (a direct BuildDeployPlan caller / test) or no externalized
	// builder is triggered → the affected builder gets base-only context, no teardown.
	// `json:"-"`: IN-PROCESS-ONLY, populated plugin-side after the HostContextJSON decode —
	// never crosses the wire (see the file doc + spec.BuilderPreresolved).
	BuilderContext map[string]BuilderPreresolved `json:"-"`

	// ActiveInitName/ActiveInit carry the MachineVenue's preresolved active init system —
	// resolved ONCE per whole-deploy compile plugin-side off the resolved-project envelope's
	// rp.Init (candy/plugin-fleet/compile.go). compileServiceSteps reads these instead of
	// re-deriving the active init per-candy or guessing via a container-oriented auto-detect
	// heuristic (which cannot disambiguate a machine venue's init from a plain custom-exec
	// service entry — proven live 2026-07-20). Nil/empty for a direct BuildDeployPlan caller /
	// test that compiles outside the seam; compileServiceSteps falls back to its own lazy
	// per-call lookup then. `json:"-"`: IN-PROCESS-ONLY, populated plugin-side — never crosses.
	ActiveInitName string        `json:"-"`
	ActiveInit     *ResolvedInit `json:"-"`
}
