package fleet

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fleet_cmd.go — the command:fleet CLI GRAMMAR (P13). The `charly fleet …` Kong tree
// moved OUT of charly core into this plugin candy; the deploy ORCHESTRATION stayed core
// behind the resolve-target-add / deploy-config host-build seams (the deploy-del-resolve +
// deploy-from-box seams are DELETED — the del resolution + from-box pod path are plugin-side,
// K-wave 2 cone R2 banks B+C; mirroring how the box-build engine stayed core behind
// HostBuild("image") in P8; the VM-disk engine moved plugin-side to candy/plugin-vm/vm_build_resolve.go — the
// former HostBuild("vm-build") is DELETED). Every leaf here is THIN: it carries
// the authored Kong flags and forwards them, as the matching sdk/spec wire request, to its
// seam via hostDeploySeam — the host reconstructs the core orchestration struct and runs
// its Run() logic VERBATIM. The lone exception is `path`, which resolves entirely plugin-side
// via kit.DefaultDeployConfigPath (no host state needed, so no seam).

// FleetCmd is the `charly fleet …` command group — the CLI grammar the compiled-in
// command:fleet plugin contributes to charly's Kong tree (dispatched in-proc via
// Invoke(OpRun) → dispatchFleetCLI).
type FleetCmd struct {
	Add FleetAddCmd `cmd:"" help:"Apply a deploy: 'host' targets the local system; any other name targets a container"`
	Del FleetDelCmd `cmd:"" help:"Tear down a deploy by name"`

	FromImage FleetFromBoxCmd `cmd:"" name:"from-box" help:"Source-less deploy from a built image's baked OCI labels (no charly.yml project). Pod by default; --cluster targets Kubernetes"`

	Export FleetExportCmd `cmd:"" help:"Export effective config as charly.yml"`
	Import FleetImportCmd `cmd:"" help:"Import charly.yml file(s) into config"`
	Path   FleetPathCmd   `cmd:"" help:"Print charly.yml file path"`
	Reset  FleetResetCmd  `cmd:"" help:"Remove charly.yml overrides"`
	Show   FleetShowCmd   `cmd:"" help:"Show current charly.yml overrides"`
	Status FleetStatusCmd `cmd:"" help:"Show sync status between charly.yml and quadlet files"`
}

// FleetAddCmd is the `charly fleet add <name> [<ref>]` grammar; it forwards to the
// deploy-add host-build seam, which runs the core add orchestration VERBATIM.
type FleetAddCmd struct {
	Name string `arg:"" help:"Deploy name ('host' for local system; any other string is a container deploy name)"`
	Ref  string `arg:"" optional:"" help:"Box or candy reference (local name, ./path.yml, or github.com/org/repo[/box/<n>|/candy/<n>][@ref])"`

	// Candy overlays (repeatable).
	AddCandy []string `name:"add-candy" help:"Extra candy to apply on top of the base image (repeatable)"`

	// Plan-level flags.
	Tag      string `name:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	DryRun   bool   `name:"dry-run" help:"Print the plan without executing"`
	NodeOnly bool   `name:"node-only" help:"Dispatch only the named node; do not descend into nested children (children of a pod can't deploy until the pod is started)"`
	Format   string `name:"format" default:"table" enum:"table,json" help:"Output format for --dry-run"`
	Pull     bool   `name:"pull" help:"Force re-fetch of remote refs / image pull"`
	Verify   bool   `name:"verify" help:"Re-run candy tests: on the host after install"`

	// Host-only gates.
	WithServices     bool   `name:"with-services" help:"Install systemd services (host target only)"`
	AllowRepoChanges bool   `name:"allow-repo-changes" help:"Allow repo config mutations (host target only)"`
	AllowRootTasks   bool   `name:"allow-root-tasks" help:"Allow arbitrary root cmd: tasks (host target only)"`
	SkipIncompatible bool   `name:"skip-incompatible" help:"Skip candies without host-matching format (host target only)"`
	BuilderImage     string `name:"builder-image" help:"Override the compile builder image"`
	DevLocalPkg      bool   `name:"dev-local-pkg" help:"Treat this as a disposable check-bed deploy: a localpkg candy whose package source cannot be found is a HARD FAILURE instead of a skip, so a bed can never silently install nothing. The deploy-side twin of 'charly box build --dev-local-pkg'; set automatically by the check-bed runner, never on an operator deploy."`
	AssumeYes        bool   `name:"assume-yes" short:"y" help:"Assume yes; implies all allow-* gates plus skip sudo preflight"`

	// Disposable + lifecycle classification (see /charly-internals:disposable).
	Disposable bool   `name:"disposable" help:"Mark this deploy disposable (authorizes autonomous charly update; writes disposable: true into charly.yml)"`
	Lifecycle  string `name:"lifecycle" help:"Informational tier tag (scratch|dev|test|qa|staging|prod|custom). NO effect on disposability — use --disposable for that."`

	// dir / externalSubstrates are INTERNAL (unexported — Kong ignores them), populated once at the
	// top of Run() from the deploy-plugins-connect preamble (dir = the host os.Getwd) and the
	// loader-threaded snapshot (externalSubstrates = the ExternalDeploySubstrates DATA set, byte-
	// exact to the host's isExternalDeploySubstrate). dispatchOne/compileNodePlans read them per node.
	dir                string
	externalSubstrates map[string]bool
}

// FleetAddCmd's Run() (the plugin-side deploy-tree WALK) lives in walk.go (K4-C walk port).

// FleetDelCmd is the `charly fleet del <name>` grammar; Run() (walk.go) drives the
// deploy-del-resolve / deploy-node-del-dispatch seams plus a direct deploykit.TearDownMembers
// call (#55 W3 A4 — the former deploy-members-down HostBuild seam is deleted). The AssumeYes field
// renders as `--assume-yes`, stated by an explicit `name:` tag rather than left to Kong's
// derivation from the field, with `-y` as the short form — the exact contract spec.FleetDelArgv
// relies on.
type FleetDelCmd struct {
	Name string `arg:"" help:"Deploy name (literal 'host' or a container deploy name)"`

	AssumeYes       bool `name:"assume-yes" short:"y" help:"Skip confirmation prompts"`
	KeepRepoChanges bool `name:"keep-repo-changes" help:"Don't revert repo config even at zero refcount"`
	KeepServices    bool `name:"keep-services" help:"Don't disable systemd units (just stop tracking)"`
	KeepImage       bool `name:"keep-image" help:"Don't remove the synthesized overlay image (container target only)"`
	DryRun          bool `name:"dry-run" help:"Print the teardown plan without executing"`

	// RequireTimerUnit marks this invocation as TIMER-DRIVEN and names the incarnation it was
	// registered for. Set only by the ephemeral TTL reaper unit (candy/plugin-fleet/ephemeral.go);
	// a human `charly fleet del` never passes it and takes a byte-identical path to before.
	//
	// It is a FLAG rather than an environment variable on purpose, and that is load-bearing: the
	// identity guarantee is enforced by the binary that RUNS, not the one that registered, so a
	// binary predating this check must not be able to reap. An env var would be silently ignored by
	// such a binary and it would delete with no incarnation check at all — fail-OPEN on exactly the
	// case the guard exists for. An unknown flag is a Kong parse error instead: measured on an
	// installed 2026.223.1347 binary, `fleet del <name> --assume-yes --require-timer-unit=x` exits
	// 80 with usage and never enters the command body, while the same command without the flag
	// parses and proceeds. The flag is its own version gate — a binary that cannot enforce the
	// guarantee cannot run the command.
	RequireTimerUnit string `name:"require-timer-unit" hidden:"" help:"Internal: the TTL reaper's own systemd unit; refuses if the recorded registration differs"`
}

// FleetFromBoxCmd is the `charly fleet from-box <ref> [name]` grammar. The pod path (default)
// runs ENTIRELY plugin-side (from_box_pod.go — a source-less deploy from an image's baked OCI
// labels, reaching deploy:pod's OpConfigSetup directly, no HostBuild round-trip); the --cluster
// path (Cone A shape 3) is handled plugin-side too — see deploy_from_box.go — reaching the kubernetes
// cluster lookup + the deploy:kubernetes substrate directly.
type FleetFromBoxCmd struct {
	Ref       string   `arg:"" help:"Full image ref (local or registry), e.g. ghcr.io/opencharly/selkies-kde-nvidia:latest"`
	Name      string   `arg:"" optional:"" help:"Deploy name (default: the image-ref basename without tag)"`
	Instance  string   `short:"i" name:"instance" help:"Instance name"`
	Env       []string `short:"e" name:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	Port      []string `short:"p" help:"Remap host port (newHost:containerPort)"`
	Cluster   string   `name:"cluster" help:"Target a Kubernetes cluster profile instead of a local pod (emits Kustomize via the Kubernetes from-box path)"`
	Namespace string   `name:"namespace" help:"Kubernetes namespace override (--cluster only)"`
}

func (c *FleetFromBoxCmd) Run() error {
	if c.Cluster != "" {
		dir, _ := os.Getwd()
		out, err := DeployFromBox(cmdCtx, cmdExec, DeployFromBoxOpts{
			ImageRef:       c.Ref,
			DeploymentName: c.Name,
			Instance:       c.Instance,
			ClusterName:    c.Cluster,
			Namespace:      c.Namespace,
			ProjectDir:     dir,
		})
		if err != nil {
			return err
		}
		name := c.Name
		if name == "" {
			name = spec.DeriveDeploymentName(c.Ref)
		}
		fmt.Fprintf(os.Stderr, "Generated Kustomize overlay for %q at %s\n  apply with: kubectl apply -k %s\n", name, out, out)
		return nil
	}
	return runFromBoxPod(c)
}

// FleetShowCmd is the `charly fleet show [box]` grammar (K4-C: runs entirely plugin-side —
// deploykit.LoadFleetConfig/DeployKey are already sdk-portable, no seam needed).
type FleetShowCmd struct {
	Box      string `arg:"" optional:"" help:"Show overrides for a specific box"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *FleetShowCmd) Run() error {
	return runFleetShow(c.Box, c.Instance)
}

// FleetExportCmd is the `charly fleet export [boxes…]` grammar (K4-C: runs plugin-side;
// --all reaches the project via the established InvokeProvider("build","project") seam).
type FleetExportCmd struct {
	Boxes  []string `arg:"" optional:"" help:"Boxes to export (default: all with overrides)"`
	Output string   `short:"o" help:"Write to file instead of stdout"`
	All    bool     `help:"Export all enabled boxes with all runtime fields"`
}

func (c *FleetExportCmd) Run() error {
	return runFleetExport(c.Boxes, c.Output, c.All)
}

// FleetImportCmd is the `charly fleet import <files…>` grammar (K4-C: runs plugin-side; the
// SAVE step writes plugin-side too — deploykit.SaveFleetConfig, #55 K4 config-write seam-collapse).
type FleetImportCmd struct {
	Files   []string `arg:"" help:"Deploy YAML files to import (merged left-to-right)"`
	Replace bool     `help:"Replace entire charly.yml instead of merging with existing"`
	Box     string   `name:"box" help:"Import only this box's config"`
}

func (c *FleetImportCmd) Run() error {
	return runFleetImport(c.Files, c.Replace, c.Box)
}

// FleetResetCmd is the `charly fleet reset [box]` grammar (K4-C: runs plugin-side; the SAVE
// step writes plugin-side too — deploykit.SaveFleetConfig, #55 K4 config-write seam-collapse).
type FleetResetCmd struct {
	Box      string `arg:"" optional:"" help:"Box to reset (omit to clear all)"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *FleetResetCmd) Run() error {
	return runFleetReset(c.Box, c.Instance)
}

// FleetStatusCmd is the `charly fleet status` grammar (K4-C: runs entirely plugin-side).
type FleetStatusCmd struct{}

func (c *FleetStatusCmd) Run() error {
	return runFleetStatus()
}

// FleetPathCmd is the `charly fleet path` grammar. It resolves the per-host deploy-overlay
// path entirely plugin-side (kit.DefaultDeployConfigPath — the SAME resolver core's
// DeployConfigPath aliases, R3), so it needs no host seam.
type FleetPathCmd struct{}

func (c *FleetPathCmd) Run() error {
	path, err := kit.DefaultDeployConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
