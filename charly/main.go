package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/opencharly/spec/exitcode"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/proc"
	"golang.org/x/term"
)

// CLI defines the command-line interface structure
type CLI struct {
	// Plugins holds the subcommands contributed by COMMAND-class providers — the 6th
	// provider seam. Populated from collectCommandPlugins() before kong.Parse (below)
	// and embedded into the grammar by Kong (anonymous kong.Plugins embed). Empty
	// today; non-machinery commands migrate into command providers in Phase 1-4.
	kong.Plugins

	// Host enables "run this command on a remote machine" semantics.
	// When set, charly re-execs itself over SSH on the target host:
	//
	//   charly --host o.example.org status        # runs `charly status` on o.example.org
	//   charly --host o vm list                   # alias lookup via `charly settings set hosts.o …`
	//
	// Commands marked LocalOnly (settings, version, ssh tunnel) are
	// not re-execed — they always run on the local machine. See
	// charly/host_exec.go for the exec dispatch.
	Host             string   `name:"host" env:"CHARLY_HOST" help:"Remote host (alias or user@host[:port]) to run this command on via SSH"`
	HostIdentityFile string   `name:"host-identity-file" env:"CHARLY_HOST_IDENTITY_FILE" help:"SSH identity file for --host" type:"path"`
	HostOption       []string `name:"host-option" env:"CHARLY_HOST_OPTION" help:"OpenSSH option for --host (repeatable, KEY=VALUE)"`

	// Dir is the project directory that every build-mode command resolves
	// charly.yml / candy/ relative to. Default is the process
	// cwd. Useful for MCP servers and remote agents that run outside a
	// project checkout — set CHARLY_PROJECT_DIR or pass -C / --dir to point at
	// a mounted project root. Build-mode commands call os.Getwd()
	// unconditionally; when this flag is set, main() chdirs before Kong's
	// ctx.Run() so every existing call site picks up the change.
	Dir string `short:"C" name:"dir" env:"CHARLY_PROJECT_DIR" help:"Project directory containing charly.yml (default: cwd)" type:"path"`

	// Repo points charly at a remote git repo as the project source instead
	// of cwd / --dir. Spec is OWNER/REPO[@REF] (auto-prefixed with
	// github.com/) or HOST/OWNER/REPO[@REF]. The literal "default" expands
	// to opencharly/charly. main() resolves this to a local cache path
	// (~/.cache/charly/repos/...) and falls through into the existing --dir
	// chdir block, so every os.Getwd() site Just Works. Mutually exclusive
	// with --dir.
	Repo string `name:"repo" env:"CHARLY_PROJECT_REPO" placeholder:"OWNER/REPO[@REF]" help:"Read charly.yml from a remote git repo (e.g. opencharly/charly). Use 'default' for opencharly/charly."`

	Box         BoxCmd                 `cmd:"" name:"box" help:"Build, generate, inspect, and pull container boxes (reads charly.yml)"`
	Plugin      PluginInternalCmd      `cmd:"" name:"__plugin" hidden:"" help:"internal: plugin server/relay plumbing"`
	AgentTarget AgentTargetInternalCmd `cmd:"" name:"__agent-target" hidden:"" help:"internal: serve generic provider gRPC over stdio"`
	CliModel    CliModelCmd            `cmd:"" name:"__cli-model" hidden:"" help:"internal: emit the CLI command tree as JSON (spec.CLIModel) for the out-of-process MCP bridge"`

	// __plugin-providers prints a candy's plugin.providers (one <class>:<word> per line) —
	// the single source the PKGBUILD uses to bake the host /usr/lib/charly/plugins/.providers
	// manifest from the candy declaration (so the CLI-served command:secrets word, absent from
	// the gRPC Describe, is not missed). Reuses collectPluginProviders (R3).
	PluginProviders PluginProvidersCmd `cmd:"" name:"__plugin-providers" hidden:"" help:"internal: print a candy's plugin.providers (one <class>:<word> per line)"`

	// (K3 build-tail move, coneB-pkgcmd: the former hidden __box-pkg reentry is DELETED.
	// candy/plugin-box's dispatchPkg now InvokeProviders(build:pkg) directly — the SAME shape
	// dispatchBuild already established for __box-build — so the localpkg build engine
	// (candy/plugin-build's runBoxPkg) runs entirely plugin-side, reusing the K1-loader seams
	// resolveBuildEngine already set up (LoadUnified, the scan-local host leg, resolveDistroLeg).)

	// (The former hidden `__cmd` deploy-lifecycle reentry behind `charly cmd` is DELETED — cmd.go's
	// dissolution: candy/plugin-cmd now drives the "pod-lifecycle" host-builder's op="cmd" (cmd's
	// slot in the floored pod-lifecycle-dispatch family, joining op="shell" — #55 W3 A10b unified
	// the former dedicated "pod-cmd" kind into this single op-discriminated one) directly over the
	// in-proc reverse channel, so the interactive Attach needs no hidden CLI reentry — mirroring
	// __box-build's earlier removal.)

	// (P8b: the former hidden __box-build reentry is DELETED. candy/plugin-box's dispatchBuild now
	// runs the `charly box build` body itself — NormalizeBoxArgs → remote-ref pivot
	// (buildkit.DetectRemoteBuildRef + the HostBuild("remote-image-resolve") seam) → build-activity
	// flock → InvokeProvider(build:box) → retention prune — so the CLI no longer bounces back through
	// core over HostBuild("cli"). The host-coupled
	// remainder (the remote-ref clone/cache resolve, the build-engine RESOLVE legs, the bootstrap
	// builder pre-pass) stays behind thin HostBuild seams the candy invokes.)

	// __box-list-tags is GONE (#118, coneB-volumecptags): candy/plugin-box's dispatchList now
	// reaches verb:retention directly over InvokeProvider (listImageTags, inspect_list.go) — the
	// SAME peer-dispatch pruneAfterBuild already used in the same module — instead of reentering
	// core for the store-live tag inventory. The sibling deploy-overlay inspect formats
	// (tunnel/bind_mounts) moved fully into candy/plugin-box earlier (the former
	// __box-inspect-overlay reentry is likewise DELETED — it renders from the deploy overlay + the
	// projector-filled view.Ports, no reentry). `box` now has NO remaining core CLI reentry.

	// __box-fetch / __box-refresh are GONE (K3 build-tail tail, coneB-buildremnant): candy/
	// plugin-authoring's command:fetch/command:refresh now reach the host-coupled repo resolver
	// (ResolveProjectRepo → EnsureRepoDownloaded) directly over the generic "box-fetch-resolve"
	// HostBuild seam (charly/host_build_box_fetch_resolve.go) — no reentry needed.

	// __box-labels is GONE (K3 reentry-class dissolution): candy/plugin-box's `labels` command now
	// calls kit.ResolveRuntime/ResolveLocalImageRef/InspectImageLabels directly — all pure
	// container-storage probes with zero loader coupling, unlike the pkg/inspect-overlay/list-tags/
	// fetch/refresh siblings above, which genuinely need host-coupled state and stay K5-doomed
	// reentries for now.

	// `charly version` is a DELIBERATE value/risk EXCEPTION kept core (the Version field below) — NOT
	// an "unfixable" one. RDD (2026-07-01) refuted the old chicken-and-egg claim: pkgver()'s
	// `bin/charly version` is only a convenience (the CalVer is already Taskfile-computed via
	// pkg/arch/calver.sh, and reading it from a sidecar / recomputing at the superproject root
	// sidesteps the submodule mismatch), so externalizing IS feasible. It is excluded because it sheds
	// ZERO deps, removes ~5 core lines, and would make R9's canonical identity command depend on the
	// plugin-resolution subsystem across 3 package repos — worst-value, highest-blast-radius of any
	// externalization. Operator-decided to keep core. (vm was the last such machinery command; P10
	// externalized it onto the COMPILED-IN command:vm plugin — candy/plugin-vm — which reaches the
	// config loader + deploy ledger + egress over generic seams and the libvirt/gpu/arbiter engines
	// over verb dispatch, so it OWNS its `charly vm …` CLI with no hidden core command.)

	// Every non-machinery command — the deploy-lifecycle + leaf-domain set (ssh,
	// start, stop, status, restart, update, remove, logs,
	// shell, cmd, cp, volume, service, config, bundle, reap-orphans) PLUS check
	// — is no longer a hardcoded field: each arrives via cli.Plugins as a builtin
	// CommandProvider in its own plugin_command_<name>.go (collectCommandPlugins()).
	// (mcp/secrets/udev/tmux/preempt/feature/vm/alias AND clean/settings/candy/doctor AND migrate are now
	// EXTERNAL or COMPILED-IN command CANDIES served by candy/plugin-* , dispatched via syscall.Exec
	// (out-of-process) or an in-proc command:<word> Invoke (compiled-in); see collectExternalCommandPlugins.
	// migrate/clean/settings/candy/doctor/feature/preempt/vm/alias OWN their engine/command in
	// candy/plugin-<name> (NONE uses a hidden core command): clean/settings/feature reach a shared
	// core subsystem over a generic HostBuild seam (retention/settings/feature), doctor needs no seam
	// at all (it gathers its host facts itself in hostfacts.go, peer verb:gpu/verb:credential), preempt reaches
	// its peer verb:arbiter over InvokeProvider, vm reaches config/ledger/egress over generic seams +
	// libvirt/gpu/arbiter over verb dispatch, alias reaches image labels via HostBuild("cli") reentry,
	// candy needs no seam (pure yaml via kit), migrate owns its engine. All compiled-in, so command:<word>
	// resolves at init() independent of any config — migrate must run when the config is exactly what cannot load.)
	// KongCommand() returns the existing <Name>Cmd struct verbatim, so the Run handler (and
	// the core machinery it calls) is unchanged: only the CLI registration LOCATION moved.
	// The whole `charly check` family is the compiled-in command:check plugin (candy/plugin-check),
	// dispatched as a top-level dynamic command. Only the machinery commands box / __plugin / migrate / version
	// (plus the hidden __* internals above) stay hardcoded on the CLI struct. (version stays core as a
	// deliberate value/risk EXCEPTION — NOT unfixable: RDD 2026-07-01 proved externalizing is feasible
	// but zero-value + highest-blast-radius, weakening R9's canonical identity command; operator-decided
	// to keep core. See the NOTE above the __* internals.)
	Version VersionCmd `cmd:"" help:"Print computed CalVer tag"`
}

// VersionCmd prints the computed CalVer tag
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	// The BINARY's identity (stamped at build time), NOT the wall clock.
	v := CharlyVersion()
	fmt.Println(v)
	if v == "unknown" {
		// A non-zero exit lets scripts gate on an UNSTAMPED binary (build with
		// `task build:binary`); the version is still printed to stdout above (#74).
		return fmt.Errorf("unstamped binary (version %q) — build with `task build:binary`", v)
	}
	return nil
}

// reapPlugins kills every connected out-of-process plugin server (each go-plugin
// client.Kill via the registry's closers). The host's authoritative reaper: run
// it on every charly exit path so a `__plugin serve` child is never orphaned.
// Best-effort + idempotent (Registry.Close); safe to call from a signal handler,
// a defer, and the explicit post-dispatch site.
func reapPlugins() { _ = providerRegistry.Close() }

func main() {
	// Load project .env into process environment before any config resolution.
	// Real env vars take precedence over .env values.
	if dir, err := os.Getwd(); err == nil {
		if err := hostenv.LoadProcessDotenv(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: loading .env: %v\n", err)
		}
	}

	var cli CLI
	// Pre-parse: learn the project's external COMMAND words (byte-gated, best-effort) so the
	// next line can put a Kong grammar holder in place for each BEFORE kong.Parse — an external
	// command plugin's word must be in the grammar to parse `charly <word>`, but connecting its
	// provider needs the project dir (itself a Kong flag). The provider stays UNconnected here;
	// the build+connect is lazy (dispatchExternalCommand). A project with no command plugins
	// registers nothing, so the grammar below is byte-for-byte the builtin set.
	//
	// FIRST discover plugins BAKED into the image (bake_plugin:) — their command words enter the
	// grammar with NO project to scan, so the in-container charly-mcp service's `charly mcp serve`
	// resolves the external `mcp` command (a no-op on a dev host with no baked plugins).
	discoverBakedPluginWords()
	prescanProjectCommandWords()
	// 6th seam: subcommands contributed by command providers — builtin (static KongCommand)
	// PLUS out-of-process command plugins (dynamic reflect.StructOf commands dispatched
	// manually after parse, since those structs carry no Run() for Kong to call). Top-level
	// ones embed on the CLI root; nested ones (e.g. `check kube`) embed under their parent.
	topCmds, nestedCmds, extCmdTable := collectExternalCommandPlugins()
	cmdPlugins := collectCommandPlugins()
	// `charly check` is the compiled-in command:check plugin (candy/plugin-check), dispatched as a
	// top-level dynamic command (topCmds → dispatchInProcCommand), so it needs no per-parent nested
	// wiring here — the check command family is fully plugin-owned.
	// `charly box` is a hardcoded CLI field (the core grammar spine), so — unlike check — its
	// nested command providers (the COMPILED-IN candy/plugin-box generate/validate/new/pkg words,
	// CommandParent()=="box") attach directly onto its embedded kong.Plugins here.
	cli.Box.Plugins = nestedCmds["box"]
	cmdPlugins = append(cmdPlugins, topCmds...)
	cli.Plugins = cmdPlugins
	ctx := kong.Parse(&cli,
		kong.Name("charly"),
		kong.Description("OpenCharly - the container management experience for you and your agents"),
		kong.UsageOnError(),
	)

	// --host: re-exec over SSH (unless we're running a LocalOnly
	// command like `settings`, `version`, or `ssh tunnel`). Doing
	// this AFTER Kong parse ensures --help / invalid-flag cases print
	// locally; doing it BEFORE ctx.Run() ensures no local state is
	// touched when we're about to forward the command. hostenv.ReexecOverSSH
	// (#55 coneG, relocated from sdk/kit — a host primitive, now a spec
	// fabric slice) is pure stdlib+spec — this dispatch glue resolves the
	// two charly-core-only inputs (the active controller binary, this
	// binary's CalVer identity) it cannot resolve itself and threads them in.
	if shouldReexecForHost(&cli, ctx.Command()) {
		controllerBin, err := activeCharlyBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "charly: --host %q: resolve active controller: %v\n", cli.Host, err)
			os.Exit(2)
		}
		wantTTY := term.IsTerminal(int(os.Stdin.Fd()))
		os.Exit(hostenv.ReexecOverSSH(cli.Host, cli.HostIdentityFile, cli.HostOption, controllerBin, CharlyVersion(), wantTTY))
	}

	// Resolve --repo before --dir. Both end up driving the same chdir
	// intervention below. Mutually exclusive: --repo would race with --dir.
	if cli.Repo != "" {
		if cli.Dir != "" {
			fmt.Fprintln(os.Stderr, "charly: --repo and --dir are mutually exclusive")
			os.Exit(1)
		}
		path, err := ResolveProjectRepo(cli.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "charly: cannot resolve --repo %q: %v\n", cli.Repo, err)
			os.Exit(1)
		}
		cli.Dir = path
	}

	// Honour -C / --dir / CHARLY_PROJECT_DIR (and --repo, after the resolver
	// above) before dispatch. Chdir is the single-point intervention:
	// every build-mode command reaches project files through os.Getwd(),
	// so one chdir here propagates to all of them without touching 10+
	// call sites.
	if cli.Dir != "" {
		if err := os.Chdir(cli.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "charly: cannot chdir to --dir %q: %v\n", cli.Dir, err)
			os.Exit(1)
		}
	}

	// Preflight-phase pre-pass (K5 seam-death): every PhasePreflight provider gets a chance to
	// hard-refuse this invocation BEFORE any command dispatch — candy/plugin-doctor's
	// verb:freshness-guard is the sole provider today, folding the former CheckBinaryFreshness
	// (CLAUDE.md R9 + the 2026-05-09 cuda-cudnn cache-mount incident) and CheckBinaryStamped
	// (#74: the twice-recurred unstamped-binary gate defect) checks into one Invoke. See
	// preflight_phase.go + candy/plugin-doctor/freshness.go for the full rationale.
	runPreflightPhase(ctx.Command())

	// Cleanup hygiene: install a global signal handler so that registered
	// temp-file paths are removed on SIGTERM/SIGINT/SIGHUP, and sweep any
	// /tmp/charly-* leftovers from prior SIGKILL'd charly invocations. See
	// cleanup.go for the full design.
	//
	// Plugin-leak hygiene (CLAUDE.md R3): reap every connected out-of-process
	// plugin server on exit so charly never orphans `__plugin serve` children.
	// Three layers, because os.Exit skips deferred funcs: a shutdown hook covers
	// catchable signals (Ctrl-C / `systemctl stop`), the defer covers a normal
	// return and a panic unwind, and an explicit reap after dispatch (below)
	// covers the os.Exit error / check-fail paths. SIGKILL / crash — the one
	// class none of these catch — is the plugin SDK's parent-death watch's job.
	proc.RegisterShutdownHook(reapPlugins)
	proc.InstallSignalHandler()
	proc.SweepStaleTemps()
	defer reapPlugins()

	// A dynamic command plugin's command has no Run() method, so dispatch it manually:
	// dispatchCommand routes by placement — a COMPILED-IN command candy in-proc via Invoke(ops.OpRun),
	// an OUT-OF-PROCESS one by syscall.Exec (F8) — with the pass-through args; everything else runs
	// through Kong's normal ctx.Run(). resolveCommandDispatch (not a bare table lookup) because a
	// capability that declares a subcommand catalog (F-CLI-NEST) renders ONE extra Kong path token
	// (e.g. "check live <args>") beyond its own registered table key ("check").
	var err error
	if d, sub, ok := resolveCommandDispatch(ctx.Command(), extCmdTable); ok {
		err = dispatchCommand(d, sub)
	} else {
		err = ctx.Run()
	}
	// Reap connected plugin servers NOW: every post-dispatch exit below uses
	// os.Exit (CheckFailExitCode / FatalIfErrorf), which skips the deferred
	// reapPlugins above. All plugin connections happen during dispatch, so this
	// single point covers the error AND check-fail exits. Idempotent with the
	// defer (Registry.Close nils its closers under the lock).
	reapPlugins()
	// `charly check` distinguishes "the thing under test is broken" from "the
	// command/usage/infra errored" via a distinct exit code: 0 = pass,
	// 1 = command error (Kong's FatalIfErrorf default), 2 = check checks
	// failed, 3 = skipped for an absent host prereq. The check command family is the
	// candy/plugin-check command plugin, which signals its exit code across the module
	// boundary via *exitcode.ExitCodeError (the host cannot classify the plugin's own error
	// TYPES). `charly box feature run` (core) uses the SAME exitcode.ExitCodeError contract.
	if err != nil {
		if ece, ok := errors.AsType[*exitcode.ExitCodeError](err); ok && ece.Code != 0 {
			fmt.Fprintln(os.Stderr, FormatCLIError(err))
			os.Exit(ece.Code) //nolint:gocritic // reapPlugins() called explicitly above before this os.Exit; the deferred reap is a redundant safety net
		}
	}
	ctx.FatalIfErrorf(FormatCLIError(err))
}
