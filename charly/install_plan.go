package main

// install_plan.go — the InstallPlan IR.
//
// Background (see plan file referenced in the final design): today's code
// walks Candy objects and emits Containerfile text directly in
// generate.go:writeCandySteps. That hardcodes "we're building an OCI image"
// into the generator. The IR defined here lifts the walk into structured
// data so the same plan can be consumed by:
//
//   - OCITarget        → deploy-mode pod-overlay (add_candy) Containerfile emission (charly bundle add <name>)
//   - ContainerDeploy  → deploy-mode overlay + quadlet (charly bundle add <name>)
//   - the local deploy target → deploy-mode host execution (charly bundle add host)
//
// `charly box build`/`generate` do NOT consume this IR — they emit Containerfile
// text directly via generate.go writeCandySteps→emitTasks. The IR is deploy-only.
//
// Keeping these three code paths behind one shared IR is the load-bearing
// move: every feature (service rendering, add_candy overlay, uninstall
// reversal) now lives in one place and applies to all three targets
// uniformly.
//
// This file defines only types and interfaces — no logic. The compiler that
// turns the candy manifest → InstallPlan lives in install_build.go; the emitters live
// in build_target_oci.go / deploy_target_pod.go / deploy_host_helpers.go.

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// HomeToken is the deferred-home placeholder the compiler bakes into
// home-bearing step fields (env.d values, path_append entries, shell-snippet
// destinations) instead of expanding `~`/`$HOME` against a compile-time home.
// Each DeployTarget resolves it at emit time via InstallPlan.ResolveHome with
// the home of the ACTUAL destination — img.Home for the OCI/pod-overlay build,
// the host home for the external local deploy, the GUEST home for the external vm deploy
// (resolved host-side in externalDeployTarget.apply via the guest executor). This
// is what lets a `target: vm` deploy write env.d that points at the guest
// user's home (/home/<guest-user>) rather than the host operator's home.
// The `{{.Home}}` spelling matches the existing builder-artifact convention
// (generate.go:expandBuilderPath), so the two token systems stay aligned.
const HomeToken = "{{.Home}}"

// ---------------------------------------------------------------------------
// Scope — where the effect lands on the target filesystem.
// ---------------------------------------------------------------------------

// Scope is the spec-homed enum (sdk/spec/deploy_wire.go) — aliased here so
// the whole IR keeps spelling it `Scope`/`ScopeSystem`/… unchanged. It lives in
// spec because an out-of-process deploy/step/builder plugin (through the SDK)
// constructs it for a ReverseOp it returns across the process boundary; package
// main and the SDK therefore share ONE type (R3).
type Scope = spec.Scope

const (
	ScopeSystem      = spec.ScopeSystem
	ScopeUser        = spec.ScopeUser
	ScopeUserProfile = spec.ScopeUserProfile
)

// ---------------------------------------------------------------------------
// Venue — where the step physically runs.
// ---------------------------------------------------------------------------

// Venue classifies where the step's commands actually execute. Today's
// generator has this distinction implicit in which rendering sub-pass
// produced the step (distro-tag section, format section, tasks, inline
// builder, multi-stage builder). The IR makes it explicit so the host
// target can pick the right execution strategy per step.
type Venue int

const (
	// VenueHostNative runs commands directly on the host (or as a RUN in
	// the Containerfile for the container target). The step's shell
	// commands execute natively; no isolation container.
	VenueHostNative Venue = iota

	// VenueContainerBuilder runs the step inside the existing multi-stage
	// builder image (fedora-builder / arch-builder / ...). On the
	// host target this means `podman run --user <host-uid> -v <paths>
	// <builder> bash -c "..."`. On the OCI target this is a FROM + RUN
	// pair that becomes its own build stage, with copy_artifacts pulling
	// outputs into the final image.
	VenueContainerBuilder

	// VenueSkip records the step with a reason but doesn't execute. Used
	// for container-runtime-only fields (ports:, volumes:, tunnel:, …)
	// when compiling for the host target, and for aur: on non-Arch hosts.
	VenueSkip
)

func (v Venue) String() string {
	switch v {
	case VenueHostNative:
		return "host-native"
	case VenueContainerBuilder:
		return "container-builder"
	case VenueSkip:
		return "skip"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Phase — three-phase execution within each step kind.
// ---------------------------------------------------------------------------

// Phase identifies which template phase (prepare / install / cleanup) a
// rendered step belongs to. The phase lets targets treat repo-config
// mutation (prepare) distinctly from package install — which is exactly
// the granularity --allow-repo-changes gates. A single format section in
// the candy manifest typically emits three PhasePrepare/PhaseInstall/PhaseCleanup
// steps, and the compiler tags each with the appropriate phase.
type Phase int

const (
	PhasePrepare Phase = iota
	PhaseInstall
	PhaseCleanup
)

func (p Phase) String() string {
	switch p {
	case PhasePrepare:
		return "prepare"
	case PhaseInstall:
		return "install"
	case PhaseCleanup:
		return "cleanup"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// StepKind — discriminator for InstallStep implementations.
// ---------------------------------------------------------------------------

// StepKind names the concrete type behind an InstallStep. Used for
// ledger serialization and target-specific dispatch (e.g. HostDeploy
// knows to invoke the pixi builder differently than cargo).
type StepKind string

const (
	StepKindSystemPackages  StepKind = "SystemPackages"
	StepKindBuilder         StepKind = "Builder"
	StepKindOp              StepKind = "Op"
	StepKindFile            StepKind = "File"
	StepKindServicePackaged StepKind = "ServicePackaged"
	StepKindServiceCustom   StepKind = "ServiceCustom"
	StepKindShellHook       StepKind = "ShellHook"
	StepKindShellSnippet    StepKind = "ShellSnippet"
	StepKindRepoChange      StepKind = "RepoChange"
	StepKindApkInstall      StepKind = "ApkInstall"
	StepKindLocalPkgInstall StepKind = "LocalPkgInstall"
	StepKindReboot          StepKind = "Reboot"
	StepKindExternalPlugin  StepKind = "ExternalPlugin"
)

// ---------------------------------------------------------------------------
// Gate — opt-in flag names for host-state-mutating operations.
// ---------------------------------------------------------------------------

// Gate is the name of a CLI flag that must be enabled for a given step to
// run on the host target. Steps without a gate run unconditionally; steps
// with a gate are skipped (with a warning) unless the user opts in.
//
// The OCI target ignores gates — container builds are already isolated,
// so gating would only slow down image construction without adding safety.
type Gate string

const (
	GateNone             Gate = ""
	GateAllowRepoChanges Gate = "allow-repo-changes"
	GateAllowRootTasks   Gate = "allow-root-tasks"
	GateWithServices     Gate = "with-services"
)

// ---------------------------------------------------------------------------
// ReverseOp — what the ledger records to un-do a step at teardown time.
// ---------------------------------------------------------------------------

// ReverseOpKind + ReverseOp are spec-homed (sdk/spec/deploy_wire.go) and
// aliased here. They live in spec because an out-of-process deploy/step/builder
// plugin (through the SDK) RETURNS ReverseOps across the process boundary for
// the host to record + replay; package main and the SDK share ONE type (R3).
// ReverseOpPluginScript is the generic recordable kind such a plugin returns.
type ReverseOpKind = spec.ReverseOpKind

const (
	ReverseOpPackageRemove  = spec.ReverseOpPackageRemove
	ReverseOpCargoUninstall = spec.ReverseOpCargoUninstall
	ReverseOpNpmUninstallG  = spec.ReverseOpNpmUninstallG
	ReverseOpPixiEnvRemove  = spec.ReverseOpPixiEnvRemove
	ReverseOpRmFileSystem   = spec.ReverseOpRmFileSystem
	ReverseOpRmFileUser     = spec.ReverseOpRmFileUser
	ReverseOpRmDirRecursive = spec.ReverseOpRmDirRecursive
	ReverseOpServiceDisable = spec.ReverseOpServiceDisable
	ReverseOpServiceRemove  = spec.ReverseOpServiceRemove
	ReverseOpRemoveDropin   = spec.ReverseOpRemoveDropin
	ReverseOpRestoreEnabled = spec.ReverseOpRestoreEnabled
	ReverseOpRemoveManaged  = spec.ReverseOpRemoveManaged
	ReverseOpRemoveEnvdFile = spec.ReverseOpRemoveEnvdFile
	ReverseOpRemoveRepoFile = spec.ReverseOpRemoveRepoFile
	ReverseOpCoprDisable    = spec.ReverseOpCoprDisable
	ReverseOpPluginScript   = spec.ReverseOpPluginScript
)

// ReverseOp is the spec-homed teardown action (see ReverseOpKind above).
type ReverseOp = spec.ReverseOp

// ---------------------------------------------------------------------------
// InstallStep — the primary IR element. Each step has one concrete type.
// ---------------------------------------------------------------------------

// InstallStep is the common interface every concrete step implements.
// Consumers (OCITarget / the local deploy target) switch on Kind() to dispatch
// to the right rendering or execution path.
type InstallStep interface {
	// Kind returns the step's concrete type discriminator.
	Kind() StepKind

	// Scope classifies where the effect lands on the target filesystem.
	Scope() Scope

	// Venue classifies where the commands physically execute.
	Venue() Venue

	// RequiresGate names the opt-in flag that must be enabled, or
	// GateNone if the step can run unconditionally. Only consulted by
	// host-target emission; the OCI target ignores gates.
	RequiresGate() Gate

	// Reverse returns the teardown actions this step contributes to the
	// ledger. Called at install time (not at teardown) so the ledger
	// captures the exact reversal actions tied to the specific artifacts
	// created. Empty return value means no reversal is recorded (e.g.
	// phases that leave no state).
	Reverse() []ReverseOp
}

// ---------------------------------------------------------------------------
// SystemPackagesStep — rpm: / deb: / pac: package install.
// ---------------------------------------------------------------------------

// RepoSpec carries a structured repo entry from the candy manifest. Matches the
// existing raw shape used by templates in the embedded build vocabulary (charly/charly.yml); the host target
// reads this to decide whether --allow-repo-changes is required.
type RepoSpec struct {
	// Fields captured verbatim from the candy manifest. Different formats use
	// different subsets (rpm has url/id/key; deb has suite/components;
	// pac has url/keys). Carried as map so the template-rendered host
	// and container forms can pick what they need.
	Raw map[string]any
}

// SystemPackagesStep installs packages via a distro package manager. One
// step typically expands to (PhasePrepare + PhaseInstall + PhaseCleanup)
// stages; the compiler emits one SystemPackagesStep per phase so the host
// target can gate PhasePrepare on --allow-repo-changes.
type SystemPackagesStep struct {
	Format   string     // "rpm" | "deb" | "pac"
	Phase    Phase      // PhasePrepare | PhaseInstall | PhaseCleanup
	Packages []string   // package names (for Reverse)
	Repos    []RepoSpec // repository entries from the candy manifest (drives PhasePrepare)
	Options  []string   // format-specific install flags
	Copr     []string   // RPM-only: COPR repos to enable/disable
	Modules  []string   // RPM-only: DNF modules to enable
	Exclude  []string   // RPM-only: packages to exclude
	Keys     []string   // PAC-only: GPG keys to trust

	// CacheMounts and RawInstallContext are passed to template rendering.
	// These are populated by the compiler and consumed by the OCI target;
	// the host target ignores CacheMounts (no BuildKit outside containers).
	CacheMount        []CacheMountSpec
	RawInstallContext map[string]any
}

func (s *SystemPackagesStep) Kind() StepKind { return StepKindSystemPackages }
func (s *SystemPackagesStep) Scope() Scope   { return ScopeSystem }
func (s *SystemPackagesStep) Venue() Venue   { return VenueHostNative }

func (s *SystemPackagesStep) RequiresGate() Gate {
	// Prepare phase mutates repo config; needs opt-in. Install/cleanup
	// phases are structurally inspectable package ops, no gate.
	if s.Phase == PhasePrepare && (len(s.Repos) > 0 || len(s.Copr) > 0 || len(s.Modules) > 0) {
		return GateAllowRepoChanges
	}
	return GateNone
}

func (s *SystemPackagesStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	switch s.Phase {
	case PhaseInstall:
		if len(s.Packages) > 0 {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpPackageRemove,
				Format:  s.Format,
				Targets: s.Packages,
				Scope:   ScopeSystem,
			})
		}
	case PhasePrepare:
		// Repo/COPR additions recorded here; host target also emits a
		// separate RepoChangeStep when the PhasePrepare executes, which
		// records the concrete files that were written. The package
		// manager's COPR tracking is reversed via copr-disable.
		for _, c := range s.Copr {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpCoprDisable,
				Format:  s.Format,
				Targets: []string{c},
				Scope:   ScopeSystem,
			})
		}
	}
	return ops
}

// CacheMountSpec mirrors the BuildKit cache-mount configuration carried in
// the embedded build vocabulary's format/builder definitions (charly/charly.yml). The OCI target renders these as
// `--mount=type=cache,...`; the host target ignores them.
type CacheMountSpec struct {
	Dst     string
	Sharing string // "locked" | "private" | "shared" | ""
}

// ---------------------------------------------------------------------------
// BuilderStep — pixi / npm / cargo / aur multi-stage builds.
// ---------------------------------------------------------------------------

// ArtifactRef describes an output path from a builder that needs to be
// extracted back to the host or copied into the final OCI image.
//
// For user-scope pixi/npm/cargo on the host target: the host path is
// bind-mounted into the builder, so there's no "extraction" — the
// Artifacts list is empty because the bind-mount already put the files
// in place. For aur: the list contains /tmp/aur-pkgs/*.pkg.tar.zst,
// which the host target pulls out to a staging dir then hands to pacman.
type ArtifactRef struct {
	ContainerPath string // path inside the builder stage/container
	HostPath      string // path on the deploy target (host fs or final-image fs)
	Chown         bool   // whether to re-own to the target user
}

// BuilderStep runs a multi-stage builder (pixi/npm/cargo/aur). On the OCI
// target this emits a FROM + RUN stage plus a final-stage COPY --from.
// On the host target this emits a `podman run <builder>` with HOME-remap
// and appropriate bind-mounts.
type BuilderStep struct {
	Builder      string        // "pixi" | "npm" | "cargo" | "aur"
	BuilderImage string        // resolved image ref (e.g. "fedora-builder:2026.04")
	CandyName    string        // layer that triggered this builder (for ledger)
	CandyDir     string        // absolute layer source path (bind-mounted as /work)
	Phase        Phase         // typically PhaseInstall
	Artifacts    []ArtifactRef // outputs to extract (empty for user-scope pixi/npm/cargo; populated for aur)

	// Builder-specific template context — the compiler populates this from
	// the candy's manifest files + the embedded builder definition (charly/charly.yml).
	RawStageContext map[string]any

	// LocalPkg is the package format's localpkg contract, populated by the
	// compiler for the `aur` builder only. The aur builder produces package
	// files (.pkg.tar.zst) that the host/VM deploy targets install onto the
	// venue via the SAME config-driven transfer+install leg the localpkg step
	// uses (R3) — so the install command + package glob come from
	// the embedded `pac.local_pkg` vocabulary, never a hardcoded literal. Nil for
	// pixi/npm/cargo (home-artifact builders, no package-file install).
	LocalPkg *LocalPkgDef

	// BuilderDef is the resolved builder definition from the embedded build vocabulary (charly/charly.yml) for this builder
	// (img.BuilderConfig.Builder[Builder]), populated by the compiler. The
	// host-venue deploy targets render its phase.install.host cell via
	// renderBuilderScript — the plain-shell analog of the container stage — so the
	// host build script is config-driven, not hardcoded Go. nil only on
	// synthetic test paths that don't supply a BuilderConfig.
	BuilderDef *BuilderDef

	// PreResolvedReverse is the builder's teardown ops, resolved HOST-SIDE in the build
	// PRE-PASS (builder_preresolve.go → the externalized builder plugin's OpReverse) and stashed
	// here so Reverse() is a PURE getter — the per-builder reverse-op KIND (pixi-env-remove /
	// package-remove / …) is exactly the logic externalized out-of-process, and Reverse() runs
	// host-side (step-view projection + RunHostStep) with no registry handle. Carried across the
	// wire on InstallStepView.ReverseOps (the host-computed teardown field) and restored by
	// stepFromView. Nil for a custom candy builder / a direct compile with no pre-pass.
	PreResolvedReverse []ReverseOp
}

func (s *BuilderStep) Kind() StepKind { return StepKindBuilder }

func (s *BuilderStep) Scope() Scope {
	// aur produces system packages; others are user-scope.
	if s.Builder == "aur" {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *BuilderStep) Venue() Venue       { return VenueContainerBuilder }
func (s *BuilderStep) RequiresGate() Gate { return GateNone }

func (s *BuilderStep) Reverse() []ReverseOp {
	// A PURE getter: the builder-specific reverse-op KIND is externalized to the builder
	// plugin (OpReverse), pre-resolved host-side in the build pre-pass (builder_preresolve.go)
	// and stashed on PreResolvedReverse. Reverse() runs host-side with no registry handle
	// (step-view projection + RunHostStep), so it must not RPC — it echoes the stashed ops.
	// Nil for a custom candy builder / a direct compile with no pre-pass.
	return s.PreResolvedReverse
}

// ---------------------------------------------------------------------------
// OpStep — one entry from the candy manifest's tasks: list.
// ---------------------------------------------------------------------------

// OpStep wraps a raw Op so the IR compiler doesn't have to transform
// it further. The OCI target and host target both understand Op shapes
// (cmd / mkdir / copy / write / link / download / setcap / build) and
// emit the appropriate directives. CtxPath substitutes for /ctx/ in cmd
// bodies on the host target (container RUN directives keep /ctx via
// bind-mount, which doesn't exist on the host).
type OpStep struct {
	Op           *Op
	CandyName    string
	CandyDir     string
	CtxPath      string // absolute layer-dir path replacing "/ctx/" on host
	ResolvedUser string // uid:gid or "root" after resolveUserSpec

	// To is the home-resolved copy/download destination for the DEPLOY path.
	// The compiler tokenizes the task's `to:` (~ / ${HOME} / $HOME → {{.Home}})
	// here; InstallPlan.ResolveHome substitutes the destination's real home at
	// emit. execTask uses this (not the raw Task.To) for PutFile, so a
	// `copy: to: ${HOME}/...` lands in the actual guest/host home instead of a
	// literal "${HOME}" directory under sudo (HOME=/root). Empty when the task
	// has no `to:`. The OCI build emitter reads Task.To directly (build-time
	// ENV expands ${HOME}), so it is unaffected by this field.
	To string

	// CandyVars are the candy manifest `vars:` map propagated into the task
	// script as exports. Build-time gets these via Containerfile ENV
	// directives (emitVarsEnv); host/local-deploy time has no equivalent
	// mechanism, so the renderer emits `export K=V` lines from this field.
	// Without this, candies like `kubernetes` whose download URLs reference
	// ${K3D_VERSION} fetched an empty path-component at deploy time and
	// curl 404'd.
	CandyVars map[string]string

	// Distros is the image's distro tag chain (img.Tags), carried so the act-emit
	// enabler can pass it to a state-provision plugin verb's RenderProvisionScript
	// (e.g. the cross-distro package resolution). Empty for non-plugin ops; the
	// unix_group act ignores it, but a future package/service state-provision plugin
	// reads it.
	Distros []string
}

func (s *OpStep) Kind() StepKind { return StepKindOp }

func (s *OpStep) Scope() Scope {
	// Classify by the task's user context: anything at root is system;
	// named users and numeric non-root UIDs are user scope.
	if s.ResolvedUser == "" || s.ResolvedUser == "root" || s.ResolvedUser == "0" || s.ResolvedUser == "0:0" {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *OpStep) Venue() Venue { return VenueHostNative }

func (s *OpStep) RequiresGate() Gate {
	// Free-form cmd bodies running as root can do anything; gate them.
	// Structured verbs (mkdir/copy/write/link/download/setcap) are
	// inspectable and don't need the gate. A command step is authored as
	// `plugin: command` (command rides plugin_input) after the extraction, so gate
	// that shape too — it renders the SAME free-form shell body via emitCmd.
	if s.Scope() == ScopeSystem && s.Op != nil && (s.Op.Command != "" || s.Op.Plugin == "command") {
		return GateAllowRootTasks
	}
	return GateNone
}

func (s *OpStep) Reverse() []ReverseOp {
	if s.Op == nil {
		return nil
	}
	// Only structurally-reversible task verbs record reversal. Cmd tasks
	// are opaque shell — we can't auto-reverse them.
	switch {
	case s.Op.Copy != "" || s.Op.Write != "":
		dest := s.Op.To
		if dest == "" {
			dest = s.Op.Copy
			if dest == "" {
				dest = s.Op.Write
			}
		}
		kind := ReverseOpRmFileUser
		if s.Scope() == ScopeSystem {
			kind = ReverseOpRmFileSystem
		}
		return []ReverseOp{{
			Kind:    kind,
			Targets: []string{dest},
			Scope:   s.Scope(),
		}}
	case s.Op.Download != "":
		// When the candy author declared an explicit uninstall list,
		// use it — that's the correct target set for extract-into-a-
		// shared-dir tasks (e.g. tarballs that land multiple binaries
		// in /usr/local/bin/). Otherwise fall back to task.To.
		targets := []string{s.Op.To}
		if len(s.Op.Uninstall) > 0 {
			targets = append([]string(nil), s.Op.Uninstall...)
		}
		return []ReverseOp{{
			Kind:    reverseFileKindFor(s.Scope()),
			Targets: targets,
			Scope:   s.Scope(),
		}}
	case s.Op.Link != "":
		return []ReverseOp{{
			Kind:    reverseFileKindFor(s.Scope()),
			Targets: []string{s.Op.Link},
			Scope:   s.Scope(),
		}}
	case s.Op.Mkdir != "":
		// Directories aren't auto-removed (might contain other files);
		// only record paths for manual inspection via `charly bundle status`.
		return nil
	}
	return nil
}

func reverseFileKindFor(sc Scope) ReverseOpKind {
	if sc == ScopeSystem {
		return ReverseOpRmFileSystem
	}
	return ReverseOpRmFileUser
}

// ---------------------------------------------------------------------------
// FileStep — structured file placement (distinct from OpStep copy/write).
// ---------------------------------------------------------------------------

// FileStep places a single file. The compiler may emit these for
// candy-declared file directives that aren't wrapped as tasks (e.g.
// supervisord fragment assembly). Today's tasks.go handles most file
// placement via OpStep; FileStep exists for cases where the compiler
// synthesizes file writes that weren't in the candy manifest (e.g. service unit
// files, managed-block contents).
type FileStep struct {
	Source    string      // path to source content (under layer dir or .build/_inline/)
	Dest      string      // absolute destination path
	Mode      os.FileMode // permissions
	Owner     string      // "root" or "uid:gid" or the invoking user's name
	CandyName string      // for ledger
}

func (s *FileStep) Kind() StepKind { return StepKindFile }

func (s *FileStep) Scope() Scope {
	if pathIsSystemScoped(s.Dest) {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *FileStep) Venue() Venue       { return VenueHostNative }
func (s *FileStep) RequiresGate() Gate { return GateNone }

func (s *FileStep) Reverse() []ReverseOp {
	return []ReverseOp{{
		Kind:    reverseFileKindFor(s.Scope()),
		Targets: []string{s.Dest},
		Scope:   s.Scope(),
	}}
}

// pathIsSystemScoped returns true for paths under /etc, /usr, /var, /opt,
// /root, /boot and similar system locations. Used to classify FileStep
// scope when the compiler hasn't explicitly tagged it.
func pathIsSystemScoped(p string) bool {
	if p == "" {
		return false
	}
	systemPrefixes := []string{
		"/etc/", "/usr/", "/var/", "/opt/", "/root/",
		"/boot/", "/srv/", "/run/", "/lib/", "/sbin/", "/bin/",
	}
	for _, pref := range systemPrefixes {
		if len(p) >= len(pref) && p[:len(pref)] == pref {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// ServicePackagedStep — enable a distro-shipped systemd unit.
// ---------------------------------------------------------------------------

// ServicePackagedStep enables a systemd unit that arrived via a distro
// package (e.g. postgresql.service). Optional drop-in overrides are
// written alongside the packaged unit without touching it. This step is
// only emitted when rendering to a systemd-based init system; supervisord
// targets emit a warning-and-skip.
type ServicePackagedStep struct {
	Unit          string // e.g. "postgresql.service"
	TargetScope   Scope  // ScopeSystem → /etc/systemd/system/; ScopeUser → ~/.config/systemd/user/
	Enable        bool
	OverridesText string // rendered drop-in (.conf) content; "" if no overrides
	OverridesPath string // computed absolute path for the drop-in file
	CandyName     string // for ledger + drop-in naming
	PriorEnabled  bool   // populated at install time (before enable); used for teardown restore
}

func (s *ServicePackagedStep) Kind() StepKind { return StepKindServicePackaged }
func (s *ServicePackagedStep) Scope() Scope   { return s.TargetScope }
func (s *ServicePackagedStep) Venue() Venue   { return VenueHostNative }
func (s *ServicePackagedStep) RequiresGate() Gate {
	return GateWithServices
}

func (s *ServicePackagedStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	if s.Enable {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpServiceDisable,
			Targets: []string{s.Unit},
			Scope:   s.TargetScope,
		})
		// Record the prior state so teardown can re-enable if needed.
		if s.PriorEnabled {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpRestoreEnabled,
				Targets: []string{s.Unit},
				Scope:   s.TargetScope,
			})
		}
	}
	if s.OverridesText != "" && s.OverridesPath != "" {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpRemoveDropin,
			Targets: []string{s.OverridesPath},
			Scope:   s.TargetScope,
		})
	}
	return ops
}

// ---------------------------------------------------------------------------
// ServiceCustomStep — full custom systemd unit (or supervisord fragment).
// ---------------------------------------------------------------------------

// ServiceCustomStep writes and enables a full service definition. The
// unit text is already rendered by the compiler from the candy's generic
// `services:` entry via the target init system's service_template.
type ServiceCustomStep struct {
	Name        string // e.g. "charly-ollama-ollama"
	UnitText    string // fully rendered unit content
	UnitPath    string // absolute install path
	TargetScope Scope  // ScopeSystem or ScopeUser
	Enable      bool
	CandyName   string
}

func (s *ServiceCustomStep) Kind() StepKind { return StepKindServiceCustom }
func (s *ServiceCustomStep) Scope() Scope   { return s.TargetScope }
func (s *ServiceCustomStep) Venue() Venue   { return VenueHostNative }
func (s *ServiceCustomStep) RequiresGate() Gate {
	return GateWithServices
}

func (s *ServiceCustomStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	if s.Enable {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpServiceDisable,
			Targets: []string{s.Name},
			Scope:   s.TargetScope,
		})
	}
	ops = append(ops, ReverseOp{
		Kind:    ReverseOpServiceRemove,
		Targets: []string{s.UnitPath},
		Scope:   s.TargetScope,
	})
	return ops
}

// ---------------------------------------------------------------------------
// ShellHookStep — env: and path_append: materialized as a shell env.d file.
// ---------------------------------------------------------------------------

// ShellHookStep records the env vars and PATH contributions a candy makes
// to the user's shell environment. On the OCI target these translate to
// `ENV K=V` directives in the Containerfile. On the host target they
// become `~/.config/opencharly/env.d/<candy>.env` plus a managed block in
// the user's shell init that sources the env.d directory.
type ShellHookStep struct {
	CandyName string
	EnvVars   map[string]string
	PathAdd   []string // already {{.Home}}-substituted to absolute paths
	EnvFile   string   // computed path (~/.config/opencharly/env.d/<candy>.env); populated at install
}

func (s *ShellHookStep) Kind() StepKind     { return StepKindShellHook }
func (s *ShellHookStep) Scope() Scope       { return ScopeUserProfile }
func (s *ShellHookStep) Venue() Venue       { return VenueHostNative }
func (s *ShellHookStep) RequiresGate() Gate { return GateNone }

func (s *ShellHookStep) Reverse() []ReverseOp {
	if s.EnvFile == "" {
		return nil
	}
	return []ReverseOp{{
		Kind:    ReverseOpRemoveEnvdFile,
		Targets: []string{s.EnvFile},
		Scope:   ScopeUserProfile,
		Extra:   map[string]string{"layer": s.CandyName},
	}}
}

// ---------------------------------------------------------------------------
// ShellSnippetStep — per-candy per-shell init snippet for the candy manifest `shell:`.
// ---------------------------------------------------------------------------

// ShellSnippetStep records a per-(candy, shell) init snippet emitted from
// a candy's `shell:` block. The compiler emits one step per (candy, shell)
// pair after applying the selection rule (per-shell ByShell entry wins
// over generic, with ${SHELL_NAME} substitution).
//
// Per-target rendering:
//   - OCITarget: snippet bytes are content-address-staged and COPYed to
//     a system-wide drop-in (/etc/profile.d/charly-<candy>.sh for bash/zsh/sh,
//     /etc/fish/conf.d/charly-<candy>.fish for fish).
//   - the external local / vm deploy (kit.WalkPlans on the venue): managed-block append to
//     the user's rc file (~/.bashrc, ~/.zshrc, ~/.profile) keyed by
//     `# opencharly:begin <Marker>` fence; for fish, a per-candy drop-in at
//     ~/.config/fish/conf.d/charly-<candy>.fish (no fence needed, file IS the
//     unit). UseDropin discriminates the two paths.
//   - the external k8s substrate: does not consume the InstallPlan IR at all
//     (the host generates a Kustomize tree separately — no shell snippet).
type ShellSnippetStep struct {
	CandyName   string   // candy this snippet belongs to (also Marker source)
	Origin      string   // "<candy>" or "box" or "deploy" (for ledger refcount + LabelShell origin)
	Shell       string   // bash | zsh | fish | sh
	Snippet     string   // rendered body, ${SHELL_NAME}-substituted, ready to write
	PathAppend  []string // already rendered into Snippet by the compiler; tracked here for label round-trip / overlay
	Destination string   // resolved per-target at compile time; absolute path on the target
	Marker      string   // managed-block marker tag (= CandyName) — used by replaceOrAppendManagedBlock
	UseDropin   bool     // true: write whole file (fish, container drop-in); false: managed-block append into existing rc file
	Priority    int      // load-order hint, 0 = default
}

func (s *ShellSnippetStep) Kind() StepKind { return StepKindShellSnippet }

// Scope: SnippetStep is system-wide for build-mode (Container drop-ins live
// under /etc/profile.d/ — root-owned), and user-profile for host/vm deploy-
// mode (managed block in ~/.bashrc etc.). The compiler picks the right
// Scope when constructing the step based on the destination path; the
// step records the choice so each emitter doesn't have to re-derive it.
func (s *ShellSnippetStep) Scope() Scope {
	if pathIsSystemScoped(s.Destination) {
		return ScopeSystem
	}
	return ScopeUserProfile
}

func (s *ShellSnippetStep) Venue() Venue       { return VenueHostNative }
func (s *ShellSnippetStep) RequiresGate() Gate { return GateNone }

func (s *ShellSnippetStep) Reverse() []ReverseOp {
	if s.UseDropin {
		// Whole-file write: removal is a plain rm of the destination.
		kind := ReverseOpRmFileUser
		if pathIsSystemScoped(s.Destination) {
			kind = ReverseOpRmFileSystem
		}
		return []ReverseOp{{
			Kind:    kind,
			Targets: []string{s.Destination},
			Scope:   s.Scope(),
			Extra:   map[string]string{"layer": s.CandyName, "shell": s.Shell},
		}}
	}
	// Managed-block append: removal strips just our fence pair from the
	// existing rc file (which may belong to the user and contain unrelated
	// content). Marker carries the per-candy fence tag so multiple candies
	// can coexist in one rc file.
	return []ReverseOp{{
		Kind:    ReverseOpRemoveManaged,
		Targets: []string{s.Destination},
		Scope:   s.Scope(),
		Extra:   map[string]string{"layer": s.CandyName, "shell": s.Shell, "marker": s.Marker},
	}}
}

// ---------------------------------------------------------------------------
// RepoChangeStep — concrete record of a repo config file being written.
// ---------------------------------------------------------------------------

// RepoChangeStep records a repo config file mutation (e.g. adding
// rpmfusion-free.repo to /etc/yum.repos.d/). Distinct from
// SystemPackagesStep's PhasePrepare because the compiler often synthesizes
// these from `cmd:` tasks that happen to install a -release.rpm — we want
// them tracked separately so `charly bundle del` can reverse them precisely.
type RepoChangeStep struct {
	Format    string // "rpm" | "deb" | "pac"
	File      string // absolute path of the repo file
	Content   string // rendered repo file body
	Checksum  string // sha256 of Content, for idempotency check at install
	CandyName string
}

func (s *RepoChangeStep) Kind() StepKind     { return StepKindRepoChange }
func (s *RepoChangeStep) Scope() Scope       { return ScopeSystem }
func (s *RepoChangeStep) Venue() Venue       { return VenueHostNative }
func (s *RepoChangeStep) RequiresGate() Gate { return GateAllowRepoChanges }

func (s *RepoChangeStep) Reverse() []ReverseOp {
	return []ReverseOp{{
		Kind:    ReverseOpRemoveRepoFile,
		Targets: []string{s.File},
		Scope:   ScopeSystem,
		Extra:   map[string]string{"layer": s.CandyName, "format": s.Format},
	}}
}

// ---------------------------------------------------------------------------
// ApkInstallStep — `apk:` package format (Android app install onto a device).
// ---------------------------------------------------------------------------

// ApkInstallStep installs Android apps onto a `kind: android` device. It is
// the IR form of a candy's `apk:` package section — the apk "package format"
// (parallel to SystemPackagesStep for rpm/deb/pac, and BuilderStep for aur).
//
// Unlike every other step, an apk install lands on a RUNNING Android device,
// not the build/host filesystem. `target: android` is an EXTERNAL deploy
// substrate (F1): NO in-proc DeployTarget executes this step — the host-side
// android deploy preresolver (collectAndroidInstalls, android_deploy_preresolve.go)
// READS it to collect the apk install specs and ships them to the deploy:android
// plugin (candy/plugin-adb: apkeep + adb), which drives the device. Every
// DeployTarget SKIPS it — OCITarget emits nothing (no device at image-build time),
// and Local/Vm/Pod targets record a skip (a host/VM/pod is not an Android device).
// This is the same "wrong venue → skip" shape `aur:` uses off-Arch, expressed as a
// clean recorded skip rather than an error (an image legitimately builds without
// installing apps).
type ApkInstallStep struct {
	Packages  []ApkPackageSpec
	CandyName string
	CandyDir  string // candy source dir — anchors relative committed-APK paths
}

func (s *ApkInstallStep) Kind() StepKind { return StepKindApkInstall }

// Scope is system — installing an app mutates device-global package state.
func (s *ApkInstallStep) Scope() Scope { return ScopeSystem }

// Venue is host-native — the android deploy preresolver reads this step from the
// host (the install itself runs in the deploy:android plugin over the wire).
func (s *ApkInstallStep) Venue() Venue       { return VenueHostNative }
func (s *ApkInstallStep) RequiresGate() Gate { return GateNone }

// Reverse returns no STATIC ledger ops — Android teardown ops are DYNAMIC, recorded
// from the deploy:android plugin's OpExecute reply (the uninstall reverse ops) at
// deploy time, replayed at `charly bundle del` like any external deploy substrate.
func (s *ApkInstallStep) Reverse() []ReverseOp { return nil }

// ---------------------------------------------------------------------------
// LocalPkgInstallStep — build a bundled PKGBUILD on the host (makepkg) and
// install the resulting `.pkg.tar.zst` onto a pac-based deploy target.
// ---------------------------------------------------------------------------
//
// LocalPkgInstallStep is the IR form of a candy's `localpkg:` field — a
// pointer at a bundled Arch PKGBUILD directory (relative to the candy dir or
// the project root). It is the proper-package counterpart of the charly candy's
// ad-hoc curl-a-binary `cmd:` task: on an Arch/CachyOS DEPLOY target the
// package is built from the repo's bundled PKGBUILD on the HOST (`makepkg`),
// the resulting artifact is transferred to the target, and `pacman -U`-installed
// — so `charly` lands as the tracked `opencharly-git` package at /usr/bin/charly rather
// than an untracked binary at /usr/local/bin/charly.
//
// Like ApkInstallStep, the step is compiled REGARDLESS of target and each
// DeployTarget decides whether to execute or skip:
//   - the external local deploy (Arch/CachyOS host) and external vm deploy (Arch/CachyOS
//     guest) EXECUTE it via the RunHostStep host-engine leg (build on host → transfer →
//     pacman -U on the target).
//   - On a NON-pac deploy target the executor records a clean skip (a Fedora /
//     Debian host has no pacman; the candy's own `cmd:` task curls the binary
//     there as the documented fallback).
//   - The IMAGE build (generate.go writeCandySteps) AND the pod-overlay build-emit
//     RENDER the localpkg IMAGE install via renderLocalPkgImageInstall: a PRODUCTION
//     box DOWNLOADS the published release, a DISPOSABLE check bed BUILDS the
//     in-development package on the host and COPYs it in (both install via the SAME
//     dep-resolving install template). The pod-overlay build-emit routes through
//     candy/plugin-installstep's `step:local-pkg-install` OpEmit, which calls back the
//     host `step-emit` seam (stepEmitLocalPkgInstall) — the build engine stays in core.
//     A distro with no localpkg-capable format (LocalPkg==nil) renders nothing; the
//     candy's own COPY/curl `cmd:` task is the fallback there.
//   - the android / k8s substrates (external) SKIP it (no Arch package surface).
//
// The PKGBUILD location is resolved at EMIT time (not compile time), so the
// step carries only the author's hint (`PkgbuildRef`) plus the candy's source
// dir + the deploy project dir for the walk-up search. When no PKGBUILD is
// found the step is a no-op (the candy's existing curl/COPY task is the
// fallback).
type LocalPkgInstallStep struct {
	PkgbuildRef string // the layer's `localpkg:` value (e.g. "pkg/arch") — a hint, resolved at emit
	CandyName   string
	CandyDir    string // layer source dir — one anchor for the relative PKGBUILD search
	ProjectDir  string // the deploy project dir (os.Getwd() at deploy time) — the other anchor

	// Format is the package-format name whose `local_pkg:` config drives this
	// step (e.g. "pac"). "" when the target distro declares no localpkg-capable
	// format — the executor then treats the step as a clean no-op.
	Format string

	// LocalPkg is the format's localpkg contract resolved from the embedded build vocabulary (charly/charly.yml) at
	// compile time (DistroDef.LocalPkgFormat). It carries the build/install
	// templates, package glob, source-dir sentinel, and probe command — so the
	// executor renders every package-manager command from config instead of
	// hardcoding build/install/glob literals. The install command auto-resolves
	// the package's dependencies from the target's repos (pacman -U / dnf
	// install / apt-get install), so there is no dep-closure builder. Nil when
	// Format == "".
	LocalPkg *LocalPkgDef
}

func (s *LocalPkgInstallStep) Kind() StepKind { return StepKindLocalPkgInstall }

// Scope is system — installing a pacman package mutates global package state.
func (s *LocalPkgInstallStep) Scope() Scope { return ScopeSystem }

// Venue is host-native — makepkg runs on the host (like the aur builder), then
// the artifact is shipped to the target and installed via pacman.
func (s *LocalPkgInstallStep) Venue() Venue       { return VenueHostNative }
func (s *LocalPkgInstallStep) RequiresGate() Gate { return GateNone }

// Reverse returns no ledger ops — the package is the deploy substrate's own
// pacman-tracked package, removed (if ever) via `pacman -R opencharly-git` by
// the operator, not by deploy teardown. Mirrors ApkInstallStep's empty Reverse.
func (s *LocalPkgInstallStep) Reverse() []ReverseOp { return nil }

// RebootStep requests a reboot of the deploy target after this candy's steps.
// It is emitted (last in the candy) when a candy declares `reboot: true` —
// the canonical case is a kernel-module candy (e.g. nvidia-open-dkms) whose
// module only loads on a fresh boot with nouveau blacklisted.
//
// Only a rebootable venue acts on it: the external vm deploy reboots the guest over SSH
// (the host's RunHostStep RebootStep handler, gated on the rebootable-venue flag the vm
// substrate sets) and waits for the boot_id to change. OCITarget / PodDeployTarget have
// no machine to reboot at build time and skip it (the external k8s substrate does not
// consume the IR at all); the external local deploy refuses to reboot the host venue
// unattended (skip + note). Mirrors the ApkInstallStep "only one target executes" pattern.
type RebootStep struct {
	CandyName string
}

func (s *RebootStep) Kind() StepKind     { return StepKindReboot }
func (s *RebootStep) Scope() Scope       { return ScopeSystem }
func (s *RebootStep) Venue() Venue       { return VenueHostNative }
func (s *RebootStep) RequiresGate() Gate { return GateNone }

// Reverse is empty — a reboot is not a persistent artifact to undo.
func (s *RebootStep) Reverse() []ReverseOp { return nil }

// ---------------------------------------------------------------------------
// ExternalPluginStep — a `run: plugin: <verb>` step served by an OUT-OF-PROCESS
// plugin, executed at DEPLOY via the E3b ExecutorService reverse channel.
// ---------------------------------------------------------------------------

// ExternalPluginStep is the install-timeline IR node for a `run: plugin: <verb>`
// step whose verb is served by an EXTERNAL (out-of-process) plugin — a grpcProvider,
// not a built-in TypedStepProvider (package/service) or ProvisionActor (the
// state-provision shell verbs) or the `command` install verb. compileActOp routes
// it here; the Invoke op is picked per venue, placement-agnostic above the registry:
//
//   - the DEPLOY venue (the install runs ON the target, not into an image): BOTH
//     target:local AND target:vm externalized, so an ExternalPluginStep on a deploy is
//     reached by the external local/vm plugin's kit.WalkPlans, which routes it through the
//     host's RunHostStep (the nested ExternalPluginStep case) → executeExternalPluginStep
//     → Invoke(OpExecute) WITH the live DeployExecutor on the go-plugin broker (the E3b
//     reverse channel), so the plugin runs its deploy-context effect (RunSystem/RunUser) on
//     the real venue and RETURNS its teardown ReverseOps which the host records into the
//     ledger (record-and-replay — only recorded ops are reversed at `charly bundle del`).
//     This is the deploy-context counterpart of the build-context OpEmit leg (tasks.go
//     emitPluginFragment), reusing the SAME spec.DeployReply / ReverseOp wire as the
//     external deploy TARGET (deploy_target_external.go) — R3.
//   - EmitOCI (a BUILD venue — the pod-overlay Containerfile): Invoke(OpEmit) via the
//     SHARED emitPluginFragment, splicing the plugin's Containerfile FRAGMENT verbatim
//     — it cannot deploy-execute at build (no live venue). Identical behaviour to the
//     box-build path (writeCandySteps → emitTasks `case "plugin"`), just relocated.
//     EmitOCI is the ONLY in-proc Emit* this provider still implements.
//
// Reverse() is static-nil: the reversal ops are DYNAMIC — they come from the plugin's
// OpExecute reply at emit time, recorded by the host's RunHostStep handler into the
// CandyRecord (the externalDeployTarget record-and-replay pattern).
type ExternalPluginStep struct {
	Op           *Op      // the run: step op — Plugin (verb word) + PluginInput + RunAs
	CandyName    string   // owning candy (provenance + the ledger CandyRecord key)
	ResolvedUser string   // uid:gid or "root" after resolveUserSpec — drives Scope
	Distros      []string // the image's distro tag chain (img.Tags) for the build-emit BuildEnv
}

func (s *ExternalPluginStep) Kind() StepKind { return StepKindExternalPlugin }

// Scope classifies by the step's user context — the SAME rule OpStep uses
// (opStepScope): root/empty → system, a named/numeric non-root user → user. The
// plugin then chooses RunSystem vs RunUser on the reverse channel per its own effect;
// this scope drives only the ledger reverse-op privilege bookkeeping.
func (s *ExternalPluginStep) Scope() Scope { return opStepScope(s.ResolvedUser) }

func (s *ExternalPluginStep) Venue() Venue { return VenueHostNative }

// RequiresGate is GateNone — an external plugin verb-step is operator-authorized
// build/deploy-time plugin execution (the candy author opted into it), mirroring the
// external deploy TARGET which is ungated; the plugin picks its own privilege via
// RunSystem/RunUser on the reverse channel.
func (s *ExternalPluginStep) RequiresGate() Gate { return GateNone }

// Reverse is static-nil: the teardown ops are recorded DYNAMICALLY from the plugin's
// OpExecute DeployReply at emit time (EmitLocal/EmitVM append reply.ReverseOps to the
// CandyRecord), exactly like the external deploy target — never recomputed.
func (s *ExternalPluginStep) Reverse() []ReverseOp { return nil }

// externalStep is an EXTERNAL, plugin-CONTRIBUTED install-step KIND (F3, closes C1): a step
// whose Kind() is "external:<word>", carried OPAQUELY (Payload) and whose Scope/Venue/Gate
// come from the serving class:step plugin's DECLARED StepContract (Describe), NOT from a
// compiled-in Go case. It is the generalization ExternalPluginStep is NOT: ExternalPluginStep
// wraps a VERB Op in the ONE fixed "ExternalPlugin" kind with a Go-fixed (advisory) contract;
// externalStep is a first-class per-word kind whose contract the PLUGIN declares — the carrier
// M2 needs to externalize the builtin step kinds (the compiler emits e.g. external:system-packages
// with a package-list Payload). Its host EXECUTION funnels through the SAME OpExecute-to-the-
// serving-plugin path ExternalPluginStep uses (dispatchExternalStepOp — R3); teardown ops are
// DYNAMIC (recorded from the OpExecute reply), so Reverse() returns the recorded slice.
type externalStep struct {
	Word       string          // the reserved step word; Kind() = "external:" + Word
	ScopeV     Scope           // plugin-declared (StepContract.scope)
	VenueV     Venue           // plugin-declared (StepContract.venue)
	GateV      Gate            // plugin-declared (StepContract.gate) — the step SKIPs if the gate is not enabled
	Payload    json.RawMessage // opaque per-kind input — the OpExecute params (plugin_input for an authored step; compiler-built for M2)
	CandyName  string          // owning candy (provenance + the ledger CandyRecord key)
	reverseOps []ReverseOp     // set DYNAMICALLY from the plugin's OpExecute reply (record-and-replay)
}

func (s *externalStep) Kind() StepKind       { return StepKind(externalStepKindPrefix + s.Word) }
func (s *externalStep) Scope() Scope         { return s.ScopeV }
func (s *externalStep) Venue() Venue         { return s.VenueV }
func (s *externalStep) RequiresGate() Gate   { return s.GateV }
func (s *externalStep) Reverse() []ReverseOp { return s.reverseOps }

// externalStepKindPrefix marks a StepKind string as an external (plugin-contributed) kind:
// "external:<word>". isExternalStepKind tests it. The open default arms in the walk
// (kit.WalkPlans) + the host RunHostStep dispatch route by this prefix with NO per-word case.
const externalStepKindPrefix = "external:"

// isExternalStepKind reports whether k is an external (plugin-contributed) step kind.
func isExternalStepKind(k StepKind) bool { return strings.HasPrefix(string(k), externalStepKindPrefix) }

// stepContract is a class:step plugin's DECLARED install-step contract (F3), decoded from its
// Describe capability (pb.StepContract / sdk.StepContract). compileActOp reads it (via the
// stepContractCarrier a provider implements) to build an externalStep carrying the
// plugin-declared Scope/Venue/Gate — the contract the host applies via the open default arm
// with NO compiled-in case.
type stepContract struct {
	Scope Scope
	Venue Venue
	Gate  Gate
	// Emits is the F-STEP-EMIT flag: the step produces a build-context Containerfile
	// FRAGMENT (the serving plugin answers Invoke(OpEmit) → spec.EmitReply.Fragment).
	// The pod-overlay OCITarget consults it via the open external-step arm — Emits=true →
	// bake the fragment; Emits=false → skip (a deploy-only external step, like apk on an
	// image build). Advisory for the DEPLOY leg (executeExternalStep ignores it); load-bearing
	// for the BUILD leg (OCITarget.emitExternalStep).
	Emits bool
}

// stepContractCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:step capability's declared StepContract. A nil/false
// return means the provider declares no step contract (every non-step capability).
type stepContractCarrier interface {
	declaredStepContract() (stepContract, bool)
}

// structuralKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's STRUCTURAL flag (F5). true → the kind's
// OpLoad returns a spec.Deploy member tree the host folds into uf.Bundle; false (or not
// implemented) → the flat F4 path (opaque body → uf.PluginKinds).
type structuralKindCarrier interface {
	isStructuralKind() bool
}

// validatingKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's VALIDATES flag (F7/C8). true → the host
// dispatches OpValidate to the kind at load (a deep plugin-owned check returning spec.Diagnostics,
// beyond the static CUE input-def gate); false (or not implemented) → only the static gate runs.
type validatingKindCarrier interface {
	isValidatingKind() bool
}

// phaseCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider compiled-in)
// that carries its declared lifecycle PHASE (F9). A provider not implementing it (e.g. a builtin
// non-plugin provider) is treated as PhaseRuntime by phaseOfProvider.
type phaseCarrier interface {
	pluginPhase() string
}

// phaseOfProvider returns a provider's lifecycle phase (F9), defaulting to sdk.PhaseRuntime for a
// provider that declares none / is not a phaseCarrier.
func phaseOfProvider(p Provider) string {
	if pc, ok := p.(phaseCarrier); ok {
		if ph := pc.pluginPhase(); ph != "" {
			return ph
		}
	}
	return sdk.PhaseRuntime
}

// scopeFromName maps a declared scope NAME (the author-friendly form a class:step plugin ships
// in its StepContract) to the internal Scope. Unknown / "system" → ScopeSystem (the safe
// default — an external step's scope is advisory for the self-exec'ing plugin, used for ledger
// + batching provenance, not host sudo-wrapping).
func scopeFromName(name string) Scope {
	switch name {
	case "user":
		return ScopeUser
	case "user-profile":
		return ScopeUserProfile
	default:
		return ScopeSystem
	}
}

// ---------------------------------------------------------------------------
// InstallPlan — the top-level IR container.
// ---------------------------------------------------------------------------

// InstallPlan is the full ordered list of steps for one candy or one
// whole-image deploy. Compiled by BuildDeployPlan and consumed by any
// DeployTarget implementation.
//
// The compiler produces one InstallPlan per candy (then merges them in
// topological order for whole-image deploys). A whole-image deploy keeps
// candy boundaries visible so the ledger can refcount which candies
// participate in which deploys — crucial for correct uninstall.
type InstallPlan struct {
	// Identity — populated by the compiler.
	DeployID string // per-deploy unique ID (hash of image + add_candy list)
	Box      string // deployable box name (or candy name for single-candy deploys)
	Version  string // candy/box CalVer version
	Distro   string // resolved host distro tag, e.g. "fedora:43"
	Candy    string // candy name when this plan is for a single candy; "" for whole-image merges

	// The ordered step sequence.
	Steps []InstallStep

	// Provenance — used by teardown and status.
	CandiesIncluded []string          // ordered layer names this plan composes (for whole-image merges)
	AddCandies      []string          // layers added on top via charly.yml add_layers: (for provenance)
	BuilderImage    string            // selected builder image for VenueContainerBuilder steps
	Meta            map[string]string // free-form metadata (builder image, glibc version, …)
}

// wireView projects the rich in-core InstallPlan onto the JSON-roundtrippable
// spec.InstallPlanView the host marshals into an external deploy/step provider's
// op.Params. The Steps interface slice round-trips through the SINGLE stepsToView /
// stepsFromView converter (step_view.go) — an external deploy/step plugin walks the
// same ordered step IR the in-proc DeployTargets walk and EXECUTES it on the venue (R3;
// proven by the step-IR round-trip test). The remaining fields are identity + provenance.
func (p *InstallPlan) wireView() spec.InstallPlanView {
	if p == nil {
		return spec.InstallPlanView{}
	}
	return spec.InstallPlanView{
		DeployID:        p.DeployID,
		Box:             p.Box,
		Version:         p.Version,
		Distro:          p.Distro,
		Candy:           p.Candy,
		CandiesIncluded: p.CandiesIncluded,
		AddCandies:      p.AddCandies,
		BuilderImage:    p.BuilderImage,
		Meta:            p.Meta,
		Steps:           stepsToView(p.Steps),
	}
}

// ResolveHome substitutes the deferred HomeToken with a concrete home in
// every home-bearing step field, in place. Each DeployTarget calls this once
// at emit time with the home of its real destination: img.Home for the
// OCI/pod-overlay build, the host home for the external local deploy, the GUEST home
// (SSH executor ResolveHome) for the external vm deploy. Idempotent — fields without
// the token are left untouched, so a second call is a no-op.
//
// Covered fields: ShellHookStep env values + PathAdd, ShellSnippetStep Snippet
// + Destination + PathAppend, FileStep.Dest. OpStep cmd/content bodies are
// intentionally NOT touched — `~`/`$HOME` there shell-expand at runtime on the
// destination as the deploy user, which is already correct on every venue.
// BuilderStep is also untouched — its home is resolved separately by
// renderBuilderScript against the builder/guest home (see execBuilder).
func (p *InstallPlan) ResolveHome(home string) {
	if p == nil || home == "" {
		return
	}
	sub := func(s string) string { return strings.ReplaceAll(s, HomeToken, home) }
	for _, step := range p.Steps {
		switch s := step.(type) {
		case *ShellHookStep:
			for k, v := range s.EnvVars {
				s.EnvVars[k] = sub(v)
			}
			for i, pth := range s.PathAdd {
				s.PathAdd[i] = sub(pth)
			}
		case *ShellSnippetStep:
			s.Snippet = sub(s.Snippet)
			s.Destination = sub(s.Destination)
			for i, pth := range s.PathAppend {
				s.PathAppend[i] = sub(pth)
			}
		case *FileStep:
			s.Dest = sub(s.Dest)
		case *ServiceCustomStep:
			// The systemd unit is pre-rendered at compile with {{.Home}} for
			// host/vm targets (see compileServiceSteps); resolve it — and the
			// user-scope unit install path — against the destination home here.
			s.UnitText = sub(s.UnitText)
			s.UnitPath = sub(s.UnitPath)
		case *OpStep:
			// Home-relative copy/download dest (tokenized at compile). The
			// Task body itself (cmd/content) is left alone — those shell-expand
			// $HOME at runtime as the deploy user.
			s.To = sub(s.To)
		}
	}
}

// StepsByVenue partitions the plan's steps by (Scope, Venue) tuple while
// preserving intra-partition order. Host target emission uses this to
// batch contiguous same-(scope, venue) runs into one heredoc. Not used
// by the OCI target (it walks Steps directly).
func (p *InstallPlan) StepsByVenue() []StepBatch {
	if len(p.Steps) == 0 {
		return nil
	}
	out := []StepBatch{}
	cur := StepBatch{Scope: p.Steps[0].Scope(), Venue: p.Steps[0].Venue()}
	for _, s := range p.Steps {
		if s.Scope() != cur.Scope || s.Venue() != cur.Venue {
			if len(cur.Steps) > 0 {
				out = append(out, cur)
			}
			cur = StepBatch{Scope: s.Scope(), Venue: s.Venue()}
		}
		cur.Steps = append(cur.Steps, s)
	}
	if len(cur.Steps) > 0 {
		out = append(out, cur)
	}
	return out
}

// StepBatch is a contiguous run of steps sharing the same (Scope, Venue).
// Emitted together: one sudo heredoc, one user heredoc, or one podman run
// per batch.
type StepBatch struct {
	Scope Scope
	Venue Venue
	Steps []InstallStep
}

// ---------------------------------------------------------------------------
// DeployTarget — what the emitters implement.
// ---------------------------------------------------------------------------

// EmitOpts carries cross-cutting toggles passed by command-line flags.
// Gates are checked per-step by the target; target-specific options (the
// container target's registry auth, the host target's --yes, --dry-run)
// are bundled here too.
type EmitOpts struct {
	DryRun               bool
	FormatJSON           bool // print IR as JSON on stdout instead of table
	AllowRepoChanges     bool
	AllowRootTasks       bool
	WithServices         bool
	SkipIncompatible     bool
	AssumeYes            bool // skip sudo preflight, confirmation prompts
	Verify               bool // run layer tests after install
	Pull                 bool // force re-fetch of remote refs / image pull
	BuilderImageOverride string

	// ParentExec is the DeployExecutor of the parent deployment in a
	// nested tree. Non-nil iff this target is dispatched as a child of
	// another — BundleAddCmd's tree walker builds the chain root-first
	// and passes the immediate ancestor's executor here. Targets that
	// support being nested (host, container, vm) compose their own
	// executor over ParentExec via NestedExecutor; leaf-only targets
	// (kubernetes) ignore it and error if non-nil.
	//
	// When nil, the target runs against its natural root venue
	// (ShellExecutor for host, a fresh SSHExecutor for vm, etc.)
	// — preserving the flat-schema behavior for v2 configs that happen
	// to have no `children:`.
	ParentExec DeployExecutor

	// ParentNode is the BundleNode above this target in the tree.
	// Useful for targets that need parent-level context beyond the
	// executor (e.g. a vm child wants to know its parent container's
	// name to wire network forwarding). nil at the root.
	ParentNode *BundleNode

	// Path is the dotted-path identifier of this node (e.g.
	// "stack.web.db"). Used for logging + ledger keying.
	Path string
}

// DeployTarget is the interface OCI + container-deploy + host-deploy
// emitters satisfy. Taking a slice of plans (rather than a single plan)
// lets whole-image deploys pass all per-candy plans at once and let the
// target merge them — useful because OCITarget may want to emit a single
// Containerfile for the image while the local deploy target may batch steps
// across candies.
type DeployTarget interface {
	Name() string
	Emit(plans []*InstallPlan, opts EmitOpts) error
}

// GateEnabled returns whether the given gate is permitted under opts.
// GateNone is always enabled; named gates require the corresponding
// opt-in flag.
func GateEnabled(g Gate, opts EmitOpts) bool {
	switch g {
	case GateNone:
		return true
	case GateAllowRepoChanges:
		return opts.AllowRepoChanges || opts.AssumeYes
	case GateAllowRootTasks:
		return opts.AllowRootTasks || opts.AssumeYes
	case GateWithServices:
		return opts.WithServices || opts.AssumeYes
	}
	return false
}

// ---------------------------------------------------------------------------
// Small helpers used by step types.
// ---------------------------------------------------------------------------

// extractStringSlice returns m[key] as []string or nil if absent.
// Accepts []string and []interface{} (as produced by yaml.v3) inputs.
func extractStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, len(t))
		copy(out, t)
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
