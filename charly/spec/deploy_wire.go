package spec

import "encoding/json"

// deploy_wire.go — the deploy IR wire types shared between charly's core
// (package main) and the plugin SDK / out-of-tree plugins.
//
// These types live in package spec — the ONE importable home — because BOTH
// the host (package main, via the `= spec.X` alias surface) AND an external
// deploy/step/builder plugin (through charly/plugin/sdk, which imports spec)
// construct and exchange them across the go-plugin process boundary. There is
// NO duplicate type for any of these concepts (R3): package main aliases them,
// the SDK references them directly.
//
// Moved here from package main install_plan.go: Scope, ReverseOpKind + its
// constants, and ReverseOp. Added here for the out-of-proc deploy wire:
// InstallPlanView, DeployVenue, DeployReply, DeployReplyRecord.

// ---------------------------------------------------------------------------
// Scope — where a step's / reverse-op's effect lands on the target filesystem.
// ---------------------------------------------------------------------------

// Scope classifies what kind of filesystem mutation a step makes. Steps are
// grouped by scope (and venue) when the host target batches into sudo vs user
// heredocs — mixing scopes in one batch would need per-command sudo. It is the
// integer-valued enum the ledger serializes (omitempty omits ScopeSystem=0), so
// the wire form is UNCHANGED by the move into spec.
type Scope int

const (
	// ScopeSystem mutates global host state: /etc, /usr, /var, systemd system
	// units, package DB. Requires sudo on host; emitted as USER root in the
	// Containerfile.
	ScopeSystem Scope = iota

	// ScopeUser mutates the invoking user's home or user-owned paths:
	// $HOME/.pixi, $HOME/.cargo, $HOME/.npm-global, $HOME/.local, systemd
	// user units, etc. No sudo needed on host; emitted as USER ${UID} in the
	// Containerfile.
	ScopeUser

	// ScopeUserProfile writes to the user's shell init surface:
	// ~/.bashrc / ~/.zshenv / fish conf.d + ~/.config/opencharly/env.d/.
	// Separate from ScopeUser because the host target has special handling
	// (managed blocks, shell detection) and the OCI target renders these as
	// ENV directives + path additions rather than file writes.
	ScopeUserProfile
)

func (s Scope) String() string {
	switch s {
	case ScopeSystem:
		return "system"
	case ScopeUser:
		return "user"
	case ScopeUserProfile:
		return "user-profile"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// ReverseOp — what the ledger records to un-do a step at teardown time.
// ---------------------------------------------------------------------------

// ReverseOpKind discriminates the kinds of teardown actions Reverse() produces.
// Ledger entries serialize these verbatim so a later `charly bundle del` can
// walk them without re-compiling the plan.
type ReverseOpKind string

const (
	ReverseOpPackageRemove  ReverseOpKind = "package-remove"
	ReverseOpCargoUninstall ReverseOpKind = "cargo-uninstall"
	ReverseOpNpmUninstallG  ReverseOpKind = "npm-uninstall-g"
	ReverseOpPixiEnvRemove  ReverseOpKind = "pixi-env-remove"
	ReverseOpRmFileSystem   ReverseOpKind = "rm-file-system"
	ReverseOpRmFileUser     ReverseOpKind = "rm-file-user"
	ReverseOpRmDirRecursive ReverseOpKind = "rm-dir-recursive"
	ReverseOpServiceDisable ReverseOpKind = "service-disable"
	ReverseOpServiceRemove  ReverseOpKind = "service-remove"
	ReverseOpRemoveDropin   ReverseOpKind = "remove-dropin"
	ReverseOpRestoreEnabled ReverseOpKind = "restore-enabled"
	ReverseOpRemoveManaged  ReverseOpKind = "remove-managed-block"
	ReverseOpRemoveEnvdFile ReverseOpKind = "remove-envd-file"
	ReverseOpRemoveRepoFile ReverseOpKind = "remove-repo-file"
	ReverseOpCoprDisable    ReverseOpKind = "copr-disable"

	// ReverseOpPluginScript is the GENERIC recordable reverse op an external
	// (out-of-process) deploy/step/builder plugin returns: a shell script + its
	// scope, run verbatim at teardown via the ReverseExecutor (system → sudo,
	// user → no sudo). The script lives in Extra["script"]; Scope picks the
	// privilege. It preserves the record-and-replay invariant — only RECORDED
	// ops are replayed, never recomputed — without any new struct shape.
	ReverseOpPluginScript ReverseOpKind = "plugin-script"
)

// ReverseOpPluginScriptKey is the Extra map key carrying a ReverseOpPluginScript's
// shell-script body. Exported so both the host handler and the SDK builder name
// the one key (R3 — no magic-string drift across the process boundary).
const ReverseOpPluginScriptKey = "script"

// ReverseOp is a single teardown action. Serialized into the ledger so uninstall
// can reverse a deploy without re-reading the candy manifest.
type ReverseOp struct {
	Kind    ReverseOpKind     `json:"kind"`
	Format  string            `json:"format,omitempty"`  // package format for package-remove (rpm/deb/pac)
	Targets []string          `json:"targets,omitempty"` // package names, file paths, env names, …
	Scope   Scope             `json:"scope,omitempty"`   // system vs user for disambiguation
	Extra   map[string]string `json:"extra,omitempty"`   // op-specific details (e.g. unit name, layer name, plugin-script body)

	// UninstallCmd is the rendered host-venue package-removal command for a
	// ReverseOpPackageRemove op, filled at record time from the format's
	// uninstall_template (the embedded build vocabulary, charly/charly.yml) by
	// fillReverseUninstallCmds — the deploy target has the DistroConfig at
	// install time, the teardown (which reads the persisted ledger) does not, so
	// the command is rendered up front and persisted. reverse_ops.go runs it
	// verbatim, so there is NO hardcoded per-format removal switch in the
	// teardown path.
	UninstallCmd string `json:"uninstall_cmd,omitempty"`
}

// ---------------------------------------------------------------------------
// Out-of-proc deploy wire — what an external deploy provider exchanges with the
// host across the go-plugin boundary on an OpExecute Invoke.
// ---------------------------------------------------------------------------

// InstallPlanView is the JSON-roundtrippable wire VIEW of an InstallPlan. The rich
// in-core InstallPlan (package main) carries a `Steps []InstallStep` interface slice;
// the host serializes it into the Steps field below via the SINGLE stepToView /
// stepFromView converter (package main charly/step_view.go), so an EXTERNAL deploy/step
// plugin that EXECUTES the plan walks the same ordered step IR the in-proc DeployTargets
// walk — the wire path and the in-proc path carry identical data (R3; proven by the
// step-IR round-trip test). The remaining fields are the plan's identity + provenance.
type InstallPlanView struct {
	DeployID        string            `json:"deploy_id,omitempty"`
	Box             string            `json:"box,omitempty"`
	Version         string            `json:"version,omitempty"`
	Distro          string            `json:"distro,omitempty"`
	Candy           string            `json:"candy,omitempty"`
	CandiesIncluded []string          `json:"candies_included,omitempty"`
	AddCandies      []string          `json:"add_candies,omitempty"`
	BuilderImage    string            `json:"builder_image,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`

	// Steps is the serializable per-step IR — the ordered InstallStep sequence the
	// in-core InstallPlan carries, projected onto the wire union below. An external
	// deploy/step plugin walks it and EXECUTES each step on the venue (the out-of-proc
	// twin of the local deploy target's in-proc walk), pushing files via the ExecutorService
	// PutFile leg and running shell via RunSystem/RunUser.
	Steps []InstallStepView `json:"steps,omitempty"`
}

// ---------------------------------------------------------------------------
// InstallStepView — the serializable per-step IR.
// ---------------------------------------------------------------------------

// InstallStepView is the JSON-roundtrippable wire form of ONE InstallStep. The in-core
// IR (package main install_plan.go) has 13 concrete step types behind the InstallStep
// interface; this is their tagged-union projection, discriminated by Kind (the
// StepKind string). It is a SUPERSET struct: each kind populates the subset of fields
// it carries, all `omitempty` so the wire stays compact.
//
// What it deliberately does NOT carry: Scope() / Venue() / RequiresGate() / Reverse().
// Those are METHODS computed from the stored fields on the concrete step, not stored
// data — so stepFromView reconstructs the SAME concrete Go step type and all four
// methods (and therefore the step's execute semantics + ordering) are byte-identical
// to the in-proc path. The wire view carries only the stored data; the behaviour rides
// along for free. That is the structural guarantee the in-proc and wire paths cannot
// diverge (R3).
//
// The package-main↔view mapping lives in ONE place — stepToView / stepFromView
// (charly/step_view.go) — and the step-IR round-trip test proves every kind survives
// stepFromView(stepToView(s)) DeepEqual-intact.
type InstallStepView struct {
	Kind string `json:"kind"` // the StepKind discriminator ("File","ShellHook","Op",…)

	// Derived ADVISORY fields — the step's Scope()/Venue()/RequiresGate() method
	// results, populated by stepToView so an executing plugin knows where the effect
	// lands (system → sudo / root-owned PutFile, user → no sudo), where it runs, and
	// which opt-in gate it needs, WITHOUT recomputing the rule (R3 — the rule lives on
	// the concrete step's methods, called once in stepToView). stepFromView IGNORES
	// them: the reconstructed concrete step recomputes the same values from its stored
	// fields, so these never participate in step round-trip identity.
	Scope Scope  `json:"scope,omitempty"`
	Venue int    `json:"venue,omitempty"`
	Gate  string `json:"gate,omitempty"`

	// Payload is the OPAQUE per-kind input for an EXTERNAL (plugin-contributed) step kind
	// (F3 — Kind == "external:<word>"): the bytes the host forwards verbatim as the serving
	// class:step plugin's OpExecute params. For an external kind the Scope/Venue/Gate fields
	// above are AUTHORITATIVE, not advisory — the host carries the plugin-DECLARED contract
	// (it cannot recompute it from a concrete step, there being no compiled-in case), so
	// stepFromView rebuilds the external step from these carried values + this Payload. nil
	// for every builtin (compiled-in) step kind.
	Payload json.RawMessage `json:"payload,omitempty"`

	// ReverseOps is the step's host-computed teardown ops — step.Reverse() called ONCE
	// host-side in stepToView. An OUT-OF-PROCESS plugin that EXECUTES a plugin-renderable
	// step itself (via RunSystem/RunUser/PutFile) cannot call the package-main Reverse()
	// method, so it ECHOES these into its DeployReply for record-and-replay teardown — the
	// Reverse() rule stays ONCE in package main (R3); the plugin gains zero reverse logic.
	// For the two deploy-time-stateful kinds (ServicePackaged.PriorEnabled,
	// ShellHook.EnvFile), the host captures that state on the live venue BEFORE projecting
	// the view, so the recorded ops are faithful. The HOST-ENGINE kinds (Builder /
	// LocalPkgInstall / SystemPackages / act-Op / ExternalPlugin) ride RunHostStep, which
	// returns their reverse ops separately, so the plugin ignores this field for the kinds
	// it routes to RunHostStep. Like the Scope/Venue/Gate advisory fields, stepFromView
	// IGNORES it (round-trip identity is unaffected).
	ReverseOps []ReverseOp `json:"reverse_ops,omitempty"`

	// Shared identity / provenance.
	CandyName string `json:"candy_name,omitempty"` // every kind
	CandyDir  string `json:"candy_dir,omitempty"`  // Builder / Op / ApkInstall / LocalPkgInstall

	// SystemPackagesStep + RepoChangeStep + LocalPkgInstallStep.
	Format string `json:"format,omitempty"`
	// SystemPackagesStep + BuilderStep three-phase tag (int Phase: prepare/install/cleanup).
	Phase int `json:"phase,omitempty"`

	// SystemPackagesStep.
	Packages          []string         `json:"packages,omitempty"`
	Repos             []map[string]any `json:"repos,omitempty"` // each = a RepoSpec.Raw map
	Options           []string         `json:"options,omitempty"`
	Copr              []string         `json:"copr,omitempty"`
	Modules           []string         `json:"modules,omitempty"`
	Exclude           []string         `json:"exclude,omitempty"`
	Keys              []string         `json:"keys,omitempty"`
	CacheMount        []CacheMountView `json:"cache_mount,omitempty"`
	RawInstallContext map[string]any   `json:"raw_install_context,omitempty"`

	// BuilderStep.
	Builder         string         `json:"builder,omitempty"`
	BuilderImage    string         `json:"builder_image,omitempty"`
	Artifacts       []ArtifactView `json:"artifacts,omitempty"`
	RawStageContext map[string]any `json:"raw_stage_context,omitempty"`
	BuilderDef      *BuilderDef    `json:"builder_def,omitempty"`
	// LocalPkg is shared by BuilderStep (aur) + LocalPkgInstallStep.
	LocalPkg *LocalPkg `json:"local_pkg,omitempty"`

	// OpStep + ExternalPluginStep (Op + the shared user/ctx/distro fields).
	Op           *Op               `json:"op,omitempty"`
	CtxPath      string            `json:"ctx_path,omitempty"`
	ResolvedUser string            `json:"resolved_user,omitempty"`
	To           string            `json:"to,omitempty"`
	CandyVars    map[string]string `json:"candy_vars,omitempty"`
	Distros      []string          `json:"distros,omitempty"`

	// FileStep.
	Source string `json:"source,omitempty"`
	Dest   string `json:"dest,omitempty"`
	Mode   uint32 `json:"mode,omitempty"` // os.FileMode underlying value
	Owner  string `json:"owner,omitempty"`

	// ServicePackagedStep + ServiceCustomStep.
	Unit          string `json:"unit,omitempty"`           // packaged unit name
	Name          string `json:"name,omitempty"`           // custom service name
	TargetScope   Scope  `json:"target_scope,omitempty"`   // both
	Enable        bool   `json:"enable,omitempty"`         // both
	OverridesText string `json:"overrides_text,omitempty"` // packaged drop-in
	OverridesPath string `json:"overrides_path,omitempty"` // packaged drop-in
	PriorEnabled  bool   `json:"prior_enabled,omitempty"`  // packaged
	UnitText      string `json:"unit_text,omitempty"`      // custom unit body
	UnitPath      string `json:"unit_path,omitempty"`      // custom unit path

	// ShellHookStep.
	EnvVars map[string]string `json:"env_vars,omitempty"`
	PathAdd []string          `json:"path_add,omitempty"`
	EnvFile string            `json:"env_file,omitempty"`

	// ShellSnippetStep.
	Origin      string   `json:"origin,omitempty"`
	Shell       string   `json:"shell,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
	PathAppend  []string `json:"path_append,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Marker      string   `json:"marker,omitempty"`
	UseDropin   bool     `json:"use_dropin,omitempty"`
	Priority    int      `json:"priority,omitempty"`

	// RepoChangeStep.
	File     string `json:"file,omitempty"`
	Content  string `json:"content,omitempty"`
	Checksum string `json:"checksum,omitempty"`

	// ApkInstallStep.
	ApkPackages []ApkPackageSpec `json:"apk_packages,omitempty"`

	// LocalPkgInstallStep.
	PkgbuildRef string `json:"pkgbuild_ref,omitempty"`
	ProjectDir  string `json:"project_dir,omitempty"`
}

// CacheMountView is the wire mirror of package main's CacheMountSpec (a BuildKit
// cache-mount config carried on a SystemPackagesStep). Kept tiny + spec-homed so the
// step view is fully self-describing on the SDK side.
type CacheMountView struct {
	Dst     string `json:"dst,omitempty"`
	Sharing string `json:"sharing,omitempty"`
}

// ArtifactView is the wire mirror of package main's ArtifactRef (a BuilderStep output
// path to extract back to the venue / final image).
type ArtifactView struct {
	ContainerPath string `json:"container_path,omitempty"`
	HostPath      string `json:"host_path,omitempty"`
	Chown         bool   `json:"chown,omitempty"`
}

// DeployVenue is the venue descriptor the host puts in op.Env for an external
// deploy Invoke: the deploy's name plus the merged deploy-node env (KEY=VALUE
// lines flattened to a map). The plugin reads it to locate where to apply its
// effects (e.g. a scratch dir) — the analogue of snapshotCheckEnv for a verb.
//
// Substrate carries a substrate-SPECIFIC preresolved payload a registered
// host-side deploy preresolver produced for the external substrate word (e.g.
// AndroidDeployVenue for deploy:android — the resolved adb endpoint + the apk
// install specs). It is OPAQUE to the generic externalDeployTarget (which never
// branches on the substrate); only the matching plugin decodes it. nil for an
// external substrate with no preresolver (the marker-only example).
type DeployVenue struct {
	DeployName string            `json:"deploy_name"`
	Env        map[string]string `json:"env,omitempty"`
	Substrate  json.RawMessage   `json:"substrate,omitempty"`
}

// VenueDescriptor is the SELF-CONTAINED, serializable description of a deploy venue's executor
// that a substrate LIFECYCLE plugin's OpPrepareVenue / OpTeardownExecutor returns (F6). A live
// DeployExecutor (ShellExecutor / *SSHExecutor) cannot cross the wire, so the plugin returns this
// descriptor and the HOST re-materializes the real executor from it — independently, AFTER the
// lifecycle Invoke returns — then serves THAT over the existing ExecutorService to the deploy-walk
// plugin. Kind "shell" → a host-local ShellExecutor (the SSH fields are ignored); Kind "ssh" → an
// *SSHExecutor built from User/Host/Port/Args/ConnectTimeout (the guest venue). Empty → no venue.
type VenueDescriptor struct {
	Kind           string   `json:"kind"` // "shell" | "ssh"
	User           string   `json:"user,omitempty"`
	Host           string   `json:"host,omitempty"`
	Port           int      `json:"port,omitempty"`
	Args           []string `json:"args,omitempty"`
	ConnectTimeout int      `json:"connect_timeout,omitempty"`
}

// Diagnostic is one finding from a plugin kind's deep OpValidate check (F7/C8) — a message, an
// optional dotted field PATH within the entity body, and a severity ("error" fails validation;
// "warning" is surfaced but non-fatal; empty is treated as "error").
type Diagnostic struct {
	Severity string `json:"severity,omitempty"` // "error" | "warning" (empty → error)
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
}

// Diagnostics is the OpValidate reply: the structured findings a plugin kind returns when the host
// asks it to validate an authored entity body BEYOND the static CUE input-def gate. An empty
// Diagnostics (no items) means the body is valid.
type Diagnostics struct {
	Items []Diagnostic `json:"items,omitempty"`
}

// HasErrors reports whether any item is error-severity (empty severity counts as error).
func (d Diagnostics) HasErrors() bool {
	for _, it := range d.Items {
		if it.Severity != "warning" {
			return true
		}
	}
	return false
}

// StructuralKindLoadEnv is the OpLoad invocation context (op.Env) the host threads to a
// STRUCTURAL class:kind plugin (F5 authored-member input-threading). A structural kind's
// authored RESOURCE-MEMBER children (pod/vm/k8s/local/android/group sub-entities) cannot ride
// op.Params — that JSON is unified against the plugin's CLOSED #<Kind>Input def, which the
// member subtree would violate. So the host PRE-DECODES the authored member children HOST-SIDE,
// via the SAME core buildBundleNode recursion the builtin path uses (buildResourceMemberChildren
// — one member-decode source of truth, R3), and threads the decoded subtree HERE. The plugin
// decodes only its KIND-SPECIFIC scalar config from op.Params and ATTACHES these members to its
// spec.Deploy reply — Members for a targetless kind (group), Children for a workload kind — so
// runPluginKind folds a COMPLETE Bundle (with members) into uf.Bundle, identical to the builtin
// decode. Cross-member `${HOST:…}` refs survive as literal strings resolved later by tree
// position (check_members.go), so host-side pre-decode is structure-preserving.
type StructuralKindLoadEnv struct {
	Members map[string]*Deploy `json:"members,omitempty"`

	// Standalone is the host-pre-decoded CANONICAL node channel the host threads to a structural
	// kind plugin whose value is RICH + core-referencing: candy/plugin-substrate (C2-substrate,
	// serving pod/vm/k8s/local/android → deploy/template shapes) AND candy/plugin-candy (C2-candy,
	// serving candy → candy-image/candy-layer shapes, folding into uf.Box/uf.Candy). Unlike group
	// — whose small scalar value is decoded from
	// op.Params against a self-contained #GroupInput and whose members ride Members above —
	// a substrate value is RICH + core-referencing (#Vm/#Deploy/#LibvirtDomain/… with
	// host-canonicalized shorthand like tunnel:/port:), so it cannot be re-decoded soundly
	// from op.Params by a plugin nor validated by a self-contained plugin schema. The host
	// therefore decodes the WHOLE node via the core buildBundleNode (deploy shape) /
	// decodeNodeValue (template shape) — the SINGLE decode source of truth (R3) — validates
	// it host-side against the KEPT #<Kind>Value def, and threads the canonical result here.
	// The plugin ECHOES it in its reply; the host folds the echo into uf.Bundle (deploy) or
	// the typed template map uf.Pod/uf.VM/… (template — the C2-substrate TEMPLATE fold arm
	// that extends F5's deploy-only fold). nil for group (which uses Members). RDD proved a
	// canonical spec.Deploy / spec.Vm / spec.Pod / … round-trips through JSON byte-faithfully,
	// so this thread-echo-fold is byte-equivalent to the former in-proc standaloneKind decode.
	Standalone *StandaloneLoad `json:"standalone,omitempty"`
}

// StandaloneLoad carries a structural kind's host-pre-decoded canonical node. Shape names
// which fold the host performs on the plugin's echo:
//   - "deploy" → Deploy (the full BundleNode) folds into uf.Bundle (C2-substrate);
//   - "template" → Template (the per-substrate typed value's JSON, e.g. a spec.Vm) folds into
//     the typed template map by kind (C2-substrate);
//   - "candy-image" → Box (a full IMAGE, base:/from:) folds into uf.Box (C2-candy);
//   - "candy-layer" → Candy (a LAYER fragment) folds into uf.Candy (C2-candy).
//
// Exactly one of Deploy / Template / Box / Candy is set, matching Shape. Both candy shapes are
// pre-decoded HOST-SIDE by the core candyIsImage + buildCandy (the bootstrap-critical box⊻layer
// routing that STAYS core — the discovered-candy pre-check calls it directly), so the plugin is
// a pure ECHO exactly like substrate. RDD proved a canonical spec.Box / spec.Candy round-trips
// through JSON byte-faithfully.
type StandaloneLoad struct {
	Shape    string          `json:"shape"`              // "deploy" | "template" | "candy-image" | "candy-layer"
	Deploy   *Deploy         `json:"deploy,omitempty"`   // Shape=="deploy": the full pre-decoded BundleNode
	Template json.RawMessage `json:"template,omitempty"` // Shape=="template": the pre-decoded typed template value's JSON
	Box      *Box            `json:"box,omitempty"`      // Shape=="candy-image": the pre-decoded IMAGE (spec.Box)
	Candy    *Candy          `json:"candy,omitempty"`    // Shape=="candy-layer": the pre-decoded LAYER (spec.Candy)
}

// AndroidDeployVenue is the preresolved deploy:android substrate payload the
// host's android deploy preresolver produces (in DeployVenue.Substrate) and the
// candy/plugin-adb deploy:android provider decodes. The host resolves the device
// endpoint (adb-server addr / in-pod engine+container / serial / google-play
// creds) and collects the apk install specs from the deploy's compiled plans
// (committed-APK Apk fields rewritten to ABSOLUTE host paths the plugin reads;
// package entries carry package/source/arch/app_version), so the plugin needs no
// project context and no goadb-less host resolution — it only drives the device.
type AndroidDeployVenue struct {
	AdbAddr     string           `json:"adb_addr"`
	Engine      string           `json:"engine,omitempty"`
	Container   string           `json:"container,omitempty"`
	Serial      string           `json:"serial,omitempty"`
	GoogleEmail string           `json:"google_email,omitempty"`
	GoogleToken string           `json:"google_token,omitempty"`
	Installs    []ApkPackageSpec `json:"installs,omitempty"`
	// BootTimeout / InstallDeadline / InstallInterval are the readiness +
	// install-retry windows the host ships (no magic numbers in the plugin):
	// boot gates sys.boot_completed; the install retries past PackageManager
	// post-boot init (a real synchronization condition, not a fixed sleep).
	BootTimeout     string `json:"boot_timeout,omitempty"`
	InstallDeadline string `json:"install_deadline,omitempty"`
	InstallInterval string `json:"install_interval,omitempty"`
}

// K8sDeployVenue is the preresolved deploy:k8s substrate payload the host's k8s
// deploy preresolver (charly/k8s_deploy_preresolve.go) produces in
// DeployVenue.Substrate and the candy/plugin-kube deploy:k8s provider decodes.
//
// Unlike deploy:android — where the plugin DRIVES the device and the host only
// resolves the endpoint + apk specs — the k8s Kustomize GENERATOR moved into the
// compiled-in candy/plugin-k8sgen (verb:k8sgen, C8/M13), fronted by the in-core
// GenerateK8sKustomize shim: the shim lifts the image Capabilities (read from the
// OCI labels via ExtractMetadata) to ports/uid/gid, Invokes the generator's OpEmit,
// then applies the host-side egress gate (#K8sObject / #Kustomization) + disk I/O.
// It has a SECOND in-core consumer (`charly bundle from-box --target k8s`,
// k8s_deploy_from_box.go). So the host generates the egress-validated Kustomize tree
// under .opencharly/k8s/<name>/ and ships only the resolved overlay path + tree
// root. The plugin owns the LIVE
// cluster I/O — `kubectl apply -k <OverlayPath>` at deploy, and the recorded
// teardown (`kubectl delete -k` + remove the tree) replayed at `charly bundle del`
// — the k8s analogue of plugin-adb installing apps after the host resolves the
// specs. The host generates what needs core machinery; the plugin does the live
// external-system I/O it owns.
type K8sDeployVenue struct {
	OverlayPath string `json:"overlay_path"`           // <root>/overlays/<inst> — the `kubectl apply -k` argument
	TreeRoot    string `json:"tree_root,omitempty"`    // <root> = .opencharly/k8s/<name> — removed at teardown
	KubeContext string `json:"kube_context,omitempty"` // kind:k8s template's kubeconfig_context → `kubectl --context` (empty → current-context)
	DeployName  string `json:"deploy_name,omitempty"`  // for plugin-side log messages
}

// DeployReply is the structured result an external deploy provider returns from
// an OpExecute Invoke: the teardown ops the host records into the ledger, plus a
// provenance record. The host writes both via the SAME install_ledger.go path a
// built-in Add uses — identical persistence, record-and-replay preserved.
type DeployReply struct {
	ReverseOps []ReverseOp       `json:"reverse_ops,omitempty"`
	Record     DeployReplyRecord `json:"record"`
}

// DeployReplyRecord names the ledger CandyRecord the host writes for an external
// deploy: the logical candy whose ReverseOps drive teardown, plus its version.
type DeployReplyRecord struct {
	Candy   string `json:"candy"`
	Version string `json:"version,omitempty"`
}

// ---------------------------------------------------------------------------
// Out-of-proc build-time wire — what a plugin verb/builder exchanges with the
// host across the go-plugin boundary on an OpEmit Invoke at IMAGE BUILD time.
// ---------------------------------------------------------------------------

// BuildEnv is the build-context descriptor the host puts in op.Env for an OpEmit
// Invoke at image-generation time: the image's distro tags + name, so a plugin can
// tailor its emitted Containerfile fragment per distro/arch. The build-time
// analogue of DeployVenue (deploy) / the verb check-env. Placement-agnostic: a
// builtin reads it in-proc, an external over gRPC.
type BuildEnv struct {
	Distros []string `json:"distros,omitempty"`
	Image   string   `json:"image,omitempty"`
}

// EmitReply is what a plugin verb/builder returns from an OpEmit Invoke at build
// time: a verbatim Containerfile FRAGMENT (RUN/COPY/… directives) the generator
// splices into the emitted .build/<image>/Containerfile (egress-validated with the
// rest of the Containerfile before it hits disk). This is the build-context
// counterpart of a builtin verb's ProvisionActor.RenderProvisionScript (a shell
// RUN) generalized to any directive an external plugin owns. The host appends a
// trailing newline if absent.
type EmitReply struct {
	Fragment string `json:"fragment"`
}

// StepEmitRequest is the F-STEP-EMIT HostBuild envelope for a HOST-COUPLED external step
// kind's build-context fragment. A class:step plugin whose OpEmit needs the host build ENGINE
// (the DistroDef format templates, the Generator's task/builder rendering — the machinery a
// []byte can't carry across the process boundary) calls back Executor.HostBuild("step-emit",
// StepEmitRequest{…}) during its OpEmit; the host's registered "step-emit" host-builder
// dispatches by Word to a per-word emitter that renders the fragment IN-CORE and returns it
// as an EmitReply (the reply reuses EmitReply — R3). Word is the step reserved word; Payload
// is the step's opaque per-kind input (the SAME bytes the plugin received in op.Params);
// Distros carries the image's distro tags (BuildEnv). A PURE step never sends this — it
// returns its EmitReply.Fragment directly from OpEmit. The host's per-word emitter registry
// holds one renderer per relocated host-coupled step kind (C1.2 registered system-packages).
type StepEmitRequest struct {
	Word    string          `json:"word"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Distros []string        `json:"distros,omitempty"`
}

// BuilderResolveReply is what a builder plugin returns from an OpResolve Invoke at
// image-generation time — the build-time BUILDER leg, the multi-stage counterpart of a
// verb/step's EmitReply. It serves BOTH the `external_builder:`-SELECTED out-of-tree
// builders AND the four DETECTION-builders (pixi/npm/aur/cargo, whose build-time
// multi-stage moved out of the embedded builder: vocabulary into their plugins — see
// kit.BuilderResolve). Stage is the `FROM <ref> AS <name>` block (its RUN/COPY body
// included) spliced PRE-main-FROM (emitExternalBuilderStages for external_builder;
// emitBuilderStages for detection builders); CopyArtifacts are the `COPY --from=<stage> …`
// directives spliced POST-main-FROM to pull the built artifacts into the final image;
// CopyBinary is the single builder-BINARY copy line (pixi → /usr/local/bin/pixi) the host
// emits ONCE per builder (deduped across candies, mirroring the former
// emitBuilderArtifacts copy_binary handling); InlineFragment is the in-candy RUN an INLINE
// builder (cargo) emits IN the main image (no separate FROM stage) — Stage/CopyArtifacts
// are empty for an inline builder, InlineFragment empty for a multi-stage one. All fields
// are egress-validated with the rest of the Containerfile before it hits disk.
type BuilderResolveReply struct {
	Stage          string   `json:"stage,omitempty"`
	CopyArtifacts  []string `json:"copy_artifacts,omitempty"`
	CopyBinary     string   `json:"copy_binary,omitempty"`
	InlineFragment string   `json:"inline_fragment,omitempty"`
}

// BuilderResolveInput is the OpResolve params: the RENDER CONTEXT the host computes and
// hands a builder plugin so it can render its build-time multi-stage self-contained. The
// host owns DETECTION (which candy triggers which builder — filesystem probes) and the
// serializable render context (the resolved builder image ref, the target user identity,
// the pre-rendered cache-mount flag strings); the plugin (via kit.BuilderResolve) owns the
// multi-stage TEMPLATE + the COPY-artifact/binary shape. The `external_builder:` path sends
// a MINIMAL input (Candy only — an out-of-tree builder renders a self-contained stage that
// reads none of the detection fields); the DETECTION path (pixi/npm/aur/cargo) sends the
// full context. Cache mounts arrive PRE-RENDERED (CacheMountsOwned/CacheMountsAuto) because
// the host owns the cache_mount vocab + the RenderCacheMounts helper — so the plugin needs
// no cache-mount render engine and the emitted bytes stay byte-identical to the former
// embedded-vocabulary render.
type BuilderResolveInput struct {
	Candy            string   `json:"candy"`
	Builder          string   `json:"builder,omitempty"`
	BuilderRef       string   `json:"builder_ref,omitempty"`
	StageName        string   `json:"stage_name,omitempty"`
	LayerStage       string   `json:"layer_stage,omitempty"`
	CopySrc          string   `json:"copy_src,omitempty"`
	UID              int      `json:"uid,omitempty"`
	GID              int      `json:"gid,omitempty"`
	Home             string   `json:"home,omitempty"`
	User             string   `json:"user,omitempty"`
	Manifest         string   `json:"manifest,omitempty"`
	HasLockFile      bool     `json:"has_lock_file,omitempty"`
	InstallCmd       string   `json:"install_cmd,omitempty"`
	ManylinuxFix     string   `json:"manylinux_fix,omitempty"`
	HasBuildScript   bool     `json:"has_build_script,omitempty"`
	BuildScript      string   `json:"build_script,omitempty"`
	Packages         []string `json:"packages,omitempty"`
	Options          []string `json:"options,omitempty"`
	CacheMountsOwned string   `json:"cache_mounts_owned,omitempty"`
	CacheMountsAuto  string   `json:"cache_mounts_auto,omitempty"`
	Inline           bool     `json:"inline,omitempty"`
}

// BuildRequest is the BUILD-ENGINE DISPATCH envelope (F10 HostBuild seam): what `charly box
// build` / `charly box generate` marshal into a build:box / build:generate plugin's Invoke
// (op.Params), which the plugin forwards VERBATIM to the host via Executor.HostBuild. The heavy
// engine (Generator / OCITarget / the runtime Candy graph) STAYS host-side in-proc — only this
// small envelope crosses the seam. Everything the engine reads (Config / ResolvedBox / Candy) is
// reconstructed HOST-SIDE from Dir inside the registered host-builder (exactly as
// pod_deploy_lifecycle re-runs NewGenerator(dir,…)); the fields here are the CLI-supplied inputs
// that are NOT reconstructable from Dir alone. The generate path reads only Boxes/Tag/Dir/
// IncludeDisabled; the build path additionally reads DevLocalPkg + the buildImages knobs
// (Push/Platform/Cache/NoCache/Jobs/PodmanJobs).
type BuildRequest struct {
	Boxes           []string `json:"boxes,omitempty"`            // positional box selection ("" → all enabled)
	Tag             string   `json:"tag,omitempty"`              // --tag override (empty → CalVer)
	Dir             string   `json:"dir,omitempty"`              // project dir the host reconstructs config from
	IncludeDisabled bool     `json:"include_disabled,omitempty"` // --include-disabled
	DevLocalPkg     bool     `json:"dev_local_pkg,omitempty"`    // --dev-local-pkg (localpkg from local source; build only)
	Push            bool     `json:"push,omitempty"`             // --push (build only)
	Platform        string   `json:"platform,omitempty"`         // --platform (build only)
	Cache           string   `json:"cache,omitempty"`            // --cache mode (build only)
	NoCache         bool     `json:"no_cache,omitempty"`         // --no-cache (build only)
	Jobs            int      `json:"jobs,omitempty"`             // --jobs outer concurrency (build only)
	PodmanJobs      int      `json:"podman_jobs,omitempty"`      // --podman-jobs inner concurrency (build only)
}

// BuildReply is what a build:box / build:generate plugin echoes back from its HostBuild call:
// the opaque list of artifacts the engine wrote (image refs for build, Containerfile paths for
// generate) plus a build error string (empty on success). A build FAILURE rides Error (the RPC
// itself succeeds — the reply-error convention), so the dispatcher surfaces it to the CLI.
type BuildReply struct {
	Written []string `json:"written,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// OverlayBuildRequest is the BUILD-ENGINE DISPATCH envelope for the pod-overlay build — the
// F10 "overlay" host-builder, the pod-substrate sibling of BuildRequest. It carries only the
// SERIALIZABLE scalars the host engine cannot reconstruct from Dir; everything heavy the
// engine reads (Config / ResolvedBox / Candy / DistroDef) is reconstructed HOST-SIDE from Dir
// via NewGenerator, exactly as runBoxBuild / the prior inline PrepareVenue body did.
//
// The overlay build's LIVE inputs — the deployment's compiled InstallPlans and, for a nested
// pod-in-pod overlay, the parent venue executor + node — do NOT ride this envelope: a live
// DeployExecutor is not serializable. The overlay build is dispatched IN-PROCESS host-side by
// podSubstrateLifecycle.PrepareVenue (a direct hostBuilders lookup, no gRPC hop), so those
// live inputs ride the ctx instead (the SAME pattern sdk.ContextWithExecutor uses to thread a
// live executor across the placement-invisible reverse channel). See package main
// overlayBuildInputs.
type OverlayBuildRequest struct {
	Dir              string `json:"dir,omitempty"`                // project dir (build-context root) the host reconstructs config from
	DeployName       string `json:"deploy_name,omitempty"`        // the raw deploy name (dotted for a nested pod; flattened engine-side)
	Image            string `json:"image,omitempty"`              // the base box the overlay inherits FROM (node.Image; "" → DeployName)
	Version          string `json:"version,omitempty"`            // the base image CalVer pin (node.Version; "" → newest-local)
	DryRun           bool   `json:"dry_run,omitempty"`            //
	AssumeYes        bool   `json:"assume_yes,omitempty"`         //
	AllowRepoChanges bool   `json:"allow_repo_changes,omitempty"` //
	AllowRootTasks   bool   `json:"allow_root_tasks,omitempty"`   //
	WithServices     bool   `json:"with_services,omitempty"`      //
}

// OverlayBuildReply is what the "overlay" host-builder returns: the built overlay image ref
// (== BaseImage when there was no add_candy overlay to synthesize), the resolved base image
// ref, and the flattened deploy name. The caller (PrepareVenue) uses these to print the start
// hint and persist the concrete overlay ref (saveDeployState) so config/start deploy exactly
// this overlay. A build FAILURE rides Error (the reply-error convention, like BuildReply).
type OverlayBuildReply struct {
	OverlayRef string `json:"overlay_ref,omitempty"`
	BaseImage  string `json:"base_image,omitempty"`
	DeployName string `json:"deploy_name,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Deploy-time builder-IR wire — what an externalized DETECTION-builder plugin
// (cargo/npm/pixi/aur) exchanges with the host on the OpCollectContext +
// OpReverse legs. A builder's build-time multi-stage is resolved by its OpResolve leg
// (BuilderResolveInput/BuilderResolveReply above — C10); these two carry the per-builder
// DEPLOY-TIME IR shim — the
// stage-context the compiler records on a BuilderStep, and that step's teardown
// ops — out-of-process. The host invokes BOTH in the build PRE-PASS (host-side,
// before the pure BuildDeployPlan compile reads the result).
// ---------------------------------------------------------------------------

// BuilderCollectInput is the OpCollectContext params: the host-supplied candy
// descriptor an external builder plugin reads to produce its per-candy stage
// context. The host fills the generic fields it can derive without builder-specific
// knowledge (Candy/Builder/Home always; Packages/Replaces from the builder's
// detect-config package section, used today only by aur). A builder reads the
// subset it needs (pixi uses none → a constant env; cargo/npm none).
type BuilderCollectInput struct {
	Candy    string   `json:"candy"`
	Builder  string   `json:"builder"`
	Home     string   `json:"home,omitempty"`
	Packages []string `json:"packages,omitempty"` // the builder's detect-config section packages (aur)
	Replaces []string `json:"replaces,omitempty"` // aur `replaces:` — repo packages removed before pacman -U
}

// BuilderCollectReply is the OpCollectContext reply: the builder-specific stage-context
// keys the host merges onto the base context ({layer,builder,home}) to form the
// BuilderStep.RawStageContext (e.g. pixi → {env_name}, aur → {packages,replaces}).
type BuilderCollectReply struct {
	Context map[string]any `json:"context,omitempty"`
}

// BuilderReverseInput is the OpReverse params: the candy + its resolved stage-context
// keys (the BuilderCollectReply.Context). The plugin maps these to its teardown ops —
// the builder-specific reverse-op KIND (pixi-env-remove / package-remove / …) is exactly
// the per-builder Go logic this externalization moves out-of-process.
type BuilderReverseInput struct {
	Candy   string         `json:"candy"`
	Builder string         `json:"builder"`
	Context map[string]any `json:"context,omitempty"`
}

// BuilderReverseReply is the OpReverse reply: the builder's teardown ops, stashed by
// the host onto BuilderStep.PreResolvedReverse so BuilderStep.Reverse() is a pure getter
// (no RPC at its host-side call sites). For aur the host renders UninstallCmd later via
// fillReverseUninstallCmds (the format's uninstall_template), the same as a built-in
// package-remove op — the plugin names only Kind/Format/Targets/Scope.
type BuilderReverseReply struct {
	ReverseOps []ReverseOp `json:"reverse_ops,omitempty"`
}
