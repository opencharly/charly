// Package box is the charly plugin housing the build-mode `charly box …` verb HANDLERS that are
// NESTED under the core `box` command group (P15). It is a dual-placement COMMAND plugin (F8): the
// SAME NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in
// compiled_plugins (the canonical placement, P15), or cmd/serve serves them OUT-OF-PROCESS when
// they are not.
//
// It serves TWELVE command capabilities, all NESTED under the `box` parent (CommandParent()=="box",
// so `charly box generate/validate/new/pull/build/inspect/list/labels/load/merge/reconcile/feature`
// parse + dispatch here while the authoring verbs (candy/plugin-authoring) stay separate — the core
// BoxCmd holds no verb of its own):
//
//   - command:generate — `charly box generate`: builds a spec.BuildRequest and InvokeProvider's the
//     peer COMPILED-IN build:generate word (candy/plugin-build), which runs the RESOLVE plugin-side
//     (resolveBuildEngine, K3 U6) and renders the .build/ Containerfile tree. Zero core reentry.
//
//   - command:new — `charly box new candy/project/box`: calls kit.ScaffoldCandy / kit.ScaffoldProject
//     / kit.AddBox directly (the scaffold ENGINE already lives in sdk/kit). Zero core reentry.
//
//   - command:validate — `charly box validate`: fetches the error-TOLERANT resolved-project envelope
//     (InvokeProvider("build","project",sdk.OpValidate) → spec.ValidateProjectReply, #55 step3 unit
//     3-I — relocated from the former HostBuild("validate-project")) and runs EVERY rule IN-PLUGIN
//     over it: the per-kind/op rules, the deploykit resolution-graph checks, the raw-config rules,
//     and the CUE-conformance + remote-candy rules (K-wave 2 cone R1 unit B). The only thing it
//     still asks the host is the registry question, HostBuild("validate-word-sets") — see
//     validate.go / validate_rules.go / validate_graph.go / validate_check.go /
//     validate_config_rules.go / validate_schema_rules.go.
//
//   - command:pull — `charly box pull`: runs the ensure-image work IN-PLUGIN (K3 #39 fold) by
//     INVOKING the peer compiled-in build:ensure word (candy/plugin-build) over InvokeProvider —
//     pull from registry, fall back to a local/remote build when the identifier maps to a project
//     charly.yml entry. A --tag short-name resolves its registry ref off the resolved-project
//     envelope (registry/name are tag-independent), so no loader is needed. The former core
//     BoxPullCmd + its hidden __box-pull reentry are DELETED.
//
//   - command:build — `charly box build`: runs the build body IN-PLUGIN (P8b — the former core
//     BuildCmd + its hidden __box-build reentry are DELETED). dispatchBuild normalizes the box args,
//     pivots to a remote @ref source (buildkit.DetectRemoteBuildRef detects it sdk-side; the
//     HostBuild("remote-image-resolve") seam clones/caches the source) when needed, computes the
//     CalVer tag, holds the build-activity flock (reconstructed from kit primitives), INVOKES the peer
//     compiled-in build:box word (candy/plugin-build's podman DRIVE) over InvokeProvider, and runs the
//     post-build retention prune (verb:retention, keep_images resolved plugin-side via
//     loaderkit.ResolveRetentionDefaultsViaExecutor — K-wave 2 cone R6; skipped for --push). The
//     host-coupled remainder a sdk-only candy cannot do — the remote-ref
//     clone/cache (EnsureRepoDownloaded, K1), the build-engine RESOLVE legs, the bootstrap
//     builder pre-pass — stays behind thin HostBuild seams the candy invokes.
//
//   - command:inspect — `charly box inspect`: reads the generic spec.ResolvedProject envelope
//     (InvokeProvider("build","project")) and prints the resolved box view — snake_case JSON by default,
//     scalar/box-aggregate fields per --format. The deploy-overlay formats (tunnel/bind_mounts) render
//     in-plugin off the deploy overlay + the resolved-project envelope (no reentry). See inspect_list.go.
//
//   - command:list — `charly box list <sub>`: boxes/candies/targets/services/routes/volumes/aliases
//     from the same envelope; `list tags` reaches verb:retention directly over InvokeProvider
//     (listImageTags, no core reentry — #118).
//
//   - command:labels — `charly box labels <ref>`: resolves the local image + prints its OCI labels
//     directly via sdk/kit (ResolveRuntime/ResolveLocalImageRef/InspectImageLabels) — pure
//     container-storage probes with zero loader coupling, so this needs NO core reentry (K3
//     reentry-class dissolution; the former `__box-labels` HostBuild("cli") hop is gone).
//
//   - command:load — `charly box load <target> <image>`: streams a host-store image into a RUNNING
//     pod venue's NESTED podman store (the container twin of `charly vm cp-box`), over the
//     venue-generic deploykit.TransferImageToVenue. Pure sdk/deploykit + spec/container — zero
//     HostBuild, zero InvokeProvider, zero core reentry. See box_load.go.
//
//   - command:merge — `charly box merge`: reads Registry/Tag/Merge settings for one (or every
//     merge.auto) box off the resolved-project envelope, then reaches verb:oci DIRECTLY via
//     InvokeProvider (the SAME F10 peer-dispatch leg candy/plugin-build's own post-build inline
//     merge already uses) — zero core reentry (P14: relocated from charly/merge.go).
//
//   - command:reconcile — `charly box reconcile`: aligns cross-repo `@github` git-tag pins to one
//     target version per repo. Purely sdk/kit + sdk/deploykit + sdk/spec + stdlib YAML — zero
//     HostBuild, zero InvokeProvider, zero core reentry (Cutover B unit 3+4: relocated from
//     charly/reconcile.go, which had no core-only coupling at all — the cleanest of this wave's
//     moves). See reconcile.go.
//
//   - command:feature — `charly box feature run <image>`: build-scope Agent Driven Evaluation
//     against a disposable container. The COMMAND is a box-nested capability HERE; the ENGINE lives
//     in candy/plugin-check (where the check runner is), reached over the F10 plugin↔plugin bridge
//     — dispatchFeature InvokeProvider's command:check's hidden `__feature-box` leaf (Mode:
//     "feature-box"), the SAME peer-dispatch shape command:build→build:ensure uses. This coexists
//     with candy/plugin-feature's TOP-LEVEL `command:feature` (`charly feature list/pending/validate`)
//     because the provider registry's uniqueness key is now CommandParent-aware (cone-C #44):
//     command:feature:box vs top-level command:feature. The former in-core BoxFeatureCmd/
//     BoxFeatureRunCmd + hostFeatureBox are DELETED with this move (#31).
//
// COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun), so the handlers run in charly's OWN process
// and inherit charly's real stdio natively. It imports ONLY the sdk module, never charly core.
package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.198.2131"

// boxCommandWords is the set of command words this plugin serves — all nested under `box`.
var boxCommandWords = []string{"generate", "validate", "new", "pull", "build", "inspect", "list", "labels", "load", "merge", "reconcile", "feature"}

// boxListSubcommands is the `charly box list <sub>` catalog (F-CLI-NEST), matching listSubcommands
// in inspect_list.go — hand-declared, not reflected, because dispatchList routes on a plain string
// switch rather than a real Kong struct (unlike candy/plugin-check's CheckCmd). Declared on the
// "list" capability so the host builds a REAL nested Kong grammar (restoring `charly box list
// --help`'s subcommand listing) and synthesizes a "box.list.<name>" leaf per entry for
// `charly __cli-model` / MCP tool generation (e.g. the box.list.boxes tool agents use to enumerate
// boxes) — both lost when `box list` moved off a static core Kong leaf onto this dynamic dispatch
// plugin (P15).
var boxListSubcommands = []sdk.CLISubcommand{
	{Name: "boxes", Help: "List enabled boxes"},
	{Name: "candies", Help: "List every scanned candy"},
	{Name: "targets", Help: "List build targets in dependency order"},
	{Name: "services", Help: "List candies that trigger an init system"},
	{Name: "routes", Help: "List candies that declare a route"},
	{Name: "volumes", Help: "List candies' declared volumes"},
	{Name: "aliases", Help: "List candies' declared aliases"},
	{Name: "tags", Help: "List an image's tags in local podman storage"},
}

// NewProvider returns the box command provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:generate/validate/new via sdk.NewMeta → BuildCapabilities so the
// COMPILED-IN path registers each as a command provider (the host builds its dynamic Kong grammar +
// dispatches Invoke(OpRun)). A command's args are pass-through CLI tokens, not a structured
// plugin_input, so the capabilities carry no InputDef and the plugin ships no schema. The "list"
// word ALSO declares its own boxListSubcommands catalog (F-CLI-NEST); every other word declares
// none, keeping today's flat pass-through grammar unchanged for them.
func NewMeta() pb.PluginMetaServer {
	caps := make([]sdk.ProvidedCapability, 0, len(boxCommandWords))
	for _, w := range boxCommandWords {
		pc := sdk.ProvidedCapability{Class: "command", Word: w}
		if w == "list" {
			pc.Subcommands = boxListSubcommands
		}
		caps = append(caps, pc)
	}
	return sdk.NewMeta(calver, caps, nil)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// The generate/validate handlers reach the host reverse channel (build:generate over InvokeProvider,
// validate-project over HostBuild), which is unavailable out-of-process, so this errors (like
// candy/plugin-vm's / candy/plugin-alias's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly box: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}

type provider struct{ pb.UnimplementedProviderServer }

// CommandParent is the optional interface buildUnitInProc detects on a compiled-in command
// plugin's provider (the SAME srv-interface-detection pattern registerCompiledPlugin uses for
// spec.DocParser / kit.RefsDownloader): every command word this plugin serves NESTS under the
// core `box` command group, so `charly box generate/validate/new` parse + dispatch here.
func (provider) CommandParent() string { return "box" }

// Invoke serves the box commands' Invoke(OpRun) AND the validate capability's structured Invoke(OpValidate):
//   - OpRun: recover the reverse-channel executor, decode the pass-through args, dispatch by the reserved
//     command word. In-proc dispatch runs in charly's own process (native stdio).
//   - OpValidate: the pre-build gate (core generate.go) Invokes the validate capability with a STRUCTURED
//     op (task #60 (C-refined)); run the engine over the tolerant envelope and RETURN the merged
//     spec.Diagnostics (no print, no exit) so the build gate consumes them as errors. Named exit K3
//     (when the build engine becomes plugin-build, this call becomes plugin↔plugin InvokeProvider).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("box: reach host reverse channel: %w", err)
	}
	switch req.GetOp() {
	case sdk.OpRun:
		word := req.GetReserved()
		var in struct {
			Args []string `json:"args"`
		}
		if len(req.GetParamsJson()) > 0 {
			if uerr := json.Unmarshal(req.GetParamsJson(), &in); uerr != nil {
				return nil, fmt.Errorf("box %s: decode args: %w", word, uerr)
			}
		}
		if rerr := dispatchBoxCommand(&hostClient{ctx: ctx, exec: exec}, word, in.Args); rerr != nil {
			return nil, rerr
		}
		return &pb.InvokeReply{}, nil
	case sdk.OpValidate:
		var vreq spec.ValidateProjectRequest
		if len(req.GetParamsJson()) > 0 {
			if uerr := json.Unmarshal(req.GetParamsJson(), &vreq); uerr != nil {
				return nil, fmt.Errorf("box validate op: decode request: %w", uerr)
			}
		}
		dir := vreq.Dir
		if dir == "" {
			d, derr := os.Getwd()
			if derr != nil {
				return nil, derr
			}
			dir = d
		}
		diags, verr := runValidateEngine(ctx, exec, dir, vreq.IncludeDisabled)
		if verr != nil {
			return nil, verr
		}
		out, merr := json.Marshal(diags)
		if merr != nil {
			return nil, merr
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("box: unsupported op %q", req.GetOp())
	}
}
