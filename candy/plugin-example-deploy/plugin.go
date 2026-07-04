// Package exampledeploy is the charly example DEPLOY plugin — an importable, dual-placement root package — that
// proves the FULL external deploy lifecycle over the E3b reverse channel — and, as the
// F2 witness, that an external deploy plugin RECEIVES a real multi-step InstallPlan,
// EXECUTES its steps on the venue, and PUSHES files via the reverse-channel PutFile leg.
//
// Its Invoke decodes the host's InstallPlan views (now carrying the serializable
// per-step IR) + the venue descriptor, then over the host's ExecutorService (via the
// SDK, on the go-plugin broker):
//
//   - writes the legacy apply/probe markers (the Add-ran witness);
//   - WALKS the plan's steps and EXECUTES the file-write (Op `write:`) + shell-hook
//     (env:) steps on the venue — the out-of-proc twin of the in-proc local deploy walk's in-proc
//     walk, honoring each step's advisory Scope (system → root-owned PutFile);
//   - PUSHES a distinct plugin-originated file via the new PutFile (the binary-safe
//     content-placement leg, the same one local/vm will use for units / the charly
//     binary / builder artifacts);
//
// and RETURNS a structured DeployReply carrying a plugin-script reverse op the host
// RECORDS in the ledger and REPLAYS at `charly bundle del` (zero operator side-effect —
// every path lives under disposable /tmp scratch dirs). charly host-builds it and serves
// it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport), the same path
// candy/plugin-example-external rides for verbs.
package exampledeploy

import (
	"context"
	"embed"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.180.0001"

// NewProvider returns the exampledeploy provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// markerDir derives the deploy's disposable scratch dir DETERMINISTICALLY from the
// deploy name, so Add and Update (whose unified signature carries no node env) agree
// on one path. Under /tmp → zero operator side-effect; Del's recorded reverse op
// removes it. User-owned, so the apply + teardown need no sudo.
func markerDir(deployName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, deployName)
	if safe == "" {
		safe = "default"
	}
	return path.Join("/tmp/charly-exampledeploy", safe)
}

// stepsDir is the disposable scratch root for the F2 step-execution + PutFile witnesses
// (the file-write step's authored dest, the rendered shell-hook env file, and the
// plugin-pushed file all live under it). Fixed (not per-deploy) so it matches the
// file-write step's authored absolute dest in the candy; removed wholesale at teardown.
const stepsDir = "/tmp/charly-exampledeploy-steps"

// Invoke applies the deployment on the host venue via the E3b reverse channel and
// returns the teardown ops + ledger record.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, err
	}
	plans, err := sdk.DecodeInstallPlans(req.GetParamsJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-example-deploy: decode plans: %w", err)
	}
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-example-deploy: decode venue: %w", err)
	}

	// (1) Legacy apply/probe markers — the Add-ran witness (unchanged contract).
	dir := markerDir(venue.DeployName)
	applied := dir + "/applied"
	probe := dir + "/probe"
	if err := exec.RunUser(ctx, "mkdir -p "+dir+" && : > "+applied+" && : > "+probe, nil); err != nil {
		return nil, err
	}

	// (2) The disposable steps scratch root, created user-owned so a user-scope
	// teardown `rm -rf` can remove even a root-owned file a system-scope step placed
	// inside it.
	if err := exec.RunUser(ctx, "mkdir -p "+stepsDir+"/env.d", nil); err != nil {
		return nil, err
	}

	// (3) WALK the plan's steps and EXECUTE them — the out-of-proc twin of
	// the in-proc local deploy walk's in-proc walk. The F2 legs (RunSystem/RunUser/PutFile) execute
	// every plugin-renderable step kind; the HOST-ENGINE kinds — here BuilderStep
	// (pixi/npm/cargo/aur) and LocalPkgInstallStep (makepkg + pacman/dnf/apt) — are
	// driven over the RunHostStep reverse leg: the host runs the EXISTING build
	// machinery and installs the artifact onto the venue, returning the step's teardown
	// ReverseOps which we fold into the DeployReply (record-and-replay).
	var buildReverseOps []spec.ReverseOp
	sawLocalPkg := false
	for _, p := range plans {
		for _, step := range p.Steps {
			switch step.Kind {
			case "Builder", "LocalPkgInstall":
				// HOST-ENGINE channel: the host builds (makepkg / BuilderRun) + installs onto
				// the venue (pacman -U / artifact transfer) — the machinery that stays in
				// charly's core. The plugin owns the WALK ordering; the host owns the ENGINE.
				ops, berr := exec.RunHostStep(ctx, step, nil)
				if berr != nil {
					return nil, fmt.Errorf("plugin-example-deploy: host-engine step %q (candy=%s): %w", step.Kind, step.CandyName, berr)
				}
				buildReverseOps = append(buildReverseOps, ops...)
				if step.Kind == "LocalPkgInstall" {
					sawLocalPkg = true
				}
			default:
				if err := applyStep(ctx, exec, step); err != nil {
					return nil, fmt.Errorf("plugin-example-deploy: execute step %q: %w", step.Kind, err)
				}
			}
		}
	}

	// (4) PUSH a distinct plugin-originated file via the new PutFile leg (binary-safe
	// content placement — the same primitive local/vm will use for units / the charly
	// binary / builder artifacts). User-scope (no sudo).
	pushed := stepsDir + "/pushed"
	if err := exec.PutFile(ctx, pushed, []byte("EXAMPLEDEPLOY-PUTFILE-OK\n"), 0o644, false); err != nil {
		return nil, fmt.Errorf("plugin-example-deploy: PutFile %s: %w", pushed, err)
	}

	// Teardown ops, replayed at `charly bundle del`:
	//   - the user-scope scratch-dir cleanup (markers + step witnesses);
	//   - whatever ReverseOps the F3 build steps returned (folded in — record-and-replay);
	//   - for a localpkg build, an explicit `pacman -R` of the dummy charly-f3-witness
	//     package. LocalPkgInstallStep.Reverse() is intentionally nil (the package is the
	//     substrate's own OS-tracked package), so the witness records the removal itself.
	//     ScopeSystem → the host runs it via sudo; tolerant so a partial / repeated
	//     teardown never errors.
	reverseOps := []spec.ReverseOp{
		sdk.PluginScriptReverseOp(spec.ScopeUser, "rm -rf "+dir+" "+stepsDir),
	}
	reverseOps = append(reverseOps, buildReverseOps...)
	if sawLocalPkg {
		reverseOps = append(reverseOps, sdk.PluginScriptReverseOp(spec.ScopeSystem,
			"pacman -R --noconfirm charly-f3-witness 2>/dev/null || true"))
	}
	return sdk.BuildDeployReply(reverseOps, "plugin-example-deploy", calver)
}

// applyStep executes ONE serialized InstallStep on the venue over the reverse channel.
// It handles the kinds this F2 witness exercises (Op `write:` + ShellHook); any other
// kind is a recorded no-op (this reference plugin is not the production local/vm engine
// — that is the NEXT cutover that CONSUMES this channel). It honors the step's advisory
// Scope to decide root-owned vs user PutFile, mirroring the in-proc local deploy walk.
func applyStep(ctx context.Context, exec *sdk.Executor, step spec.InstallStepView) error {
	switch step.Kind {
	case "Op":
		// A file-write step (`write: <dest>` + `content:`). Place the content at the
		// authored dest via PutFile — proving the Op step's fields round-tripped and the
		// dest is honored. ownerRoot follows the step's effective scope.
		if step.Op != nil && step.Op.Write != "" {
			return exec.PutFile(ctx, step.Op.Write, []byte(step.Op.Content),
				parseMode(step.Op.Mode, 0o644), step.Scope == spec.ScopeSystem)
		}
		return nil
	case "ShellHook":
		// A shell-hook step (env: / path_append:). Render a minimal env.d file from the
		// received EnvVars + PathAdd and place it on the venue — proving the ShellHook
		// step's data round-tripped and was acted on. Under the disposable scratch dir
		// (the production env.d-into-$HOME placement is the in-proc local deploy walk's job).
		content := renderEnvd(step.EnvVars, step.PathAdd)
		dest := stepsDir + "/env.d/" + step.CandyName + ".env"
		return exec.PutFile(ctx, dest, []byte(content), 0o644, false)
	default:
		return nil
	}
}

// renderEnvd builds a deterministic env.d body (sorted KEY=VALUE lines + a PATH export)
// from a shell-hook step's env vars + path additions.
func renderEnvd(env map[string]string, pathAdd []string) string {
	var b strings.Builder
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, env[k])
	}
	for _, p := range pathAdd {
		fmt.Fprintf(&b, "export PATH=%s:$PATH\n", p)
	}
	return b.String()
}

// parseMode parses a candy task mode string ("0644","0o755") into octal perms,
// falling back to def when empty/unparseable.
func parseMode(mode string, def uint32) uint32 {
	if mode == "" {
		return def
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(mode, "0o"), 8, 32)
	if err != nil {
		return def
	}
	return uint32(v)
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the deploy:exampledeploy capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "exampledeploy", InputDef: "#ExampledeployInput"}},
		schemaFS, "schema")
}
