package bundle

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// bundle_cmd.go — the command:bundle CLI GRAMMAR (P13). The `charly bundle …` Kong tree
// moved OUT of charly core into this plugin candy; the deploy ORCHESTRATION stayed core
// behind the resolve-target-add / deploy-del-resolve / deploy-from-box / deploy-config
// host-build seams (mirroring how the box-build engine stayed core behind HostBuild("image")
// in P8; the VM-disk engine moved plugin-side to candy/plugin-vm/vm_build_resolve.go — the
// former HostBuild("vm-build") is DELETED). Every leaf here is THIN: it carries
// the authored Kong flags and forwards them, as the matching sdk/spec wire request, to its
// seam via hostDeploySeam — the host reconstructs the core orchestration struct and runs
// its Run() logic VERBATIM. The lone exception is `path`, which resolves entirely plugin-side
// via kit.DefaultDeployConfigPath (no host state needed, so no seam).

// BundleCmd is the `charly bundle …` command group — the CLI grammar the compiled-in
// command:bundle plugin contributes to charly's Kong tree (dispatched in-proc via
// Invoke(OpRun) → dispatchBundleCLI).
type BundleCmd struct {
	Add BundleAddCmd `cmd:"" help:"Apply a deploy: 'host' targets the local system; any other name targets a container"`
	Del BundleDelCmd `cmd:"" help:"Tear down a deploy by name"`

	FromImage BundleFromBoxCmd `cmd:"" name:"from-box" help:"Source-less deploy from a built image's baked OCI labels (no charly.yml project). Pod by default; --cluster targets K8s"`

	Export BundleExportCmd `cmd:"" help:"Export effective config as charly.yml"`
	Import BundleImportCmd `cmd:"" help:"Import charly.yml file(s) into config"`
	Path   BundlePathCmd   `cmd:"" help:"Print charly.yml file path"`
	Reset  BundleResetCmd  `cmd:"" help:"Remove charly.yml overrides"`
	Show   BundleShowCmd   `cmd:"" help:"Show current charly.yml overrides"`
	Status BundleStatusCmd `cmd:"" help:"Show sync status between charly.yml and quadlet files"`
}

// BundleAddCmd is the `charly bundle add <name> [<ref>]` grammar; it forwards to the
// deploy-add host-build seam, which runs the core add orchestration VERBATIM.
type BundleAddCmd struct {
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

// BundleAddCmd's Run() (the plugin-side deploy-tree WALK) lives in walk.go (K4-C walk port).

// BundleDelCmd is the `charly bundle del <name>` grammar; Run() (walk.go) drives the
// deploy-del-resolve / deploy-node-del-dispatch seams plus a direct deploykit.TearDownMembers
// call (#55 W3 A4 — the former deploy-members-down HostBuild seam is deleted). The AssumeYes field
// renders as `--assume-yes`, stated by an explicit `name:` tag rather than left to Kong's
// derivation from the field, with `-y` as the short form — the exact contract spec.BundleDelArgv
// relies on.
type BundleDelCmd struct {
	Name string `arg:"" help:"Deploy name (literal 'host' or a container deploy name)"`

	AssumeYes       bool `name:"assume-yes" short:"y" help:"Skip confirmation prompts"`
	KeepRepoChanges bool `name:"keep-repo-changes" help:"Don't revert repo config even at zero refcount"`
	KeepServices    bool `name:"keep-services" help:"Don't disable systemd units (just stop tracking)"`
	KeepImage       bool `name:"keep-image" help:"Don't remove the synthesized overlay image (container target only)"`
	DryRun          bool `name:"dry-run" help:"Print the teardown plan without executing"`
}

// BundleFromBoxCmd is the `charly bundle from-box <ref> [name]` grammar. The pod path (default)
// forwards to the deploy-from-box host-build seam (a source-less deploy from an image's baked OCI
// labels); the --cluster path (Cone A shape 3) is handled ENTIRELY plugin-side — see
// deploy_from_box.go — reaching the k8s cluster lookup + the deploy:k8s substrate directly, no
// HostBuild round-trip for the k8s branch.
type BundleFromBoxCmd struct {
	Ref       string   `arg:"" help:"Full image ref (local or registry), e.g. ghcr.io/opencharly/selkies-kde-nvidia:latest"`
	Name      string   `arg:"" optional:"" help:"Deploy name (default: the image-ref basename without tag)"`
	Instance  string   `short:"i" name:"instance" help:"Instance name"`
	Env       []string `short:"e" name:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	Port      []string `short:"p" help:"Remap host port (newHost:containerPort)"`
	Cluster   string   `name:"cluster" help:"Target a K8s cluster profile instead of a local pod (emits Kustomize via the K8s from-box path)"`
	Namespace string   `name:"namespace" help:"K8s namespace override (--cluster only)"`
}

func (c *BundleFromBoxCmd) Run() error {
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
	return hostDeploySeam("deploy-from-box", spec.DeployFromBoxRequest{
		Ref:      c.Ref,
		Name:     c.Name,
		Instance: c.Instance,
		Env:      c.Env,
		Port:     c.Port,
	})
}

// BundleShowCmd is the `charly bundle show [box]` grammar (K4-C: runs entirely plugin-side —
// deploykit.LoadBundleConfig/DeployKey are already sdk-portable, no seam needed).
type BundleShowCmd struct {
	Box      string `arg:"" optional:"" help:"Show overrides for a specific box"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *BundleShowCmd) Run() error {
	return runBundleShow(c.Box, c.Instance)
}

// BundleExportCmd is the `charly bundle export [boxes…]` grammar (K4-C: runs plugin-side;
// --all reaches the project via the established InvokeProvider("build","project") seam).
type BundleExportCmd struct {
	Boxes  []string `arg:"" optional:"" help:"Boxes to export (default: all with overrides)"`
	Output string   `short:"o" help:"Write to file instead of stdout"`
	All    bool     `help:"Export all enabled boxes with all runtime fields"`
}

func (c *BundleExportCmd) Run() error {
	return runBundleExport(c.Boxes, c.Output, c.All)
}

// BundleImportCmd is the `charly bundle import <files…>` grammar (K4-C: runs plugin-side; the
// SAVE step writes plugin-side too — deploykit.SaveBundleConfig, #55 K4 config-write seam-collapse).
type BundleImportCmd struct {
	Files   []string `arg:"" help:"Deploy YAML files to import (merged left-to-right)"`
	Replace bool     `help:"Replace entire charly.yml instead of merging with existing"`
	Box     string   `name:"box" help:"Import only this box's config"`
}

func (c *BundleImportCmd) Run() error {
	return runBundleImport(c.Files, c.Replace, c.Box)
}

// BundleResetCmd is the `charly bundle reset [box]` grammar (K4-C: runs plugin-side; the SAVE
// step writes plugin-side too — deploykit.SaveBundleConfig, #55 K4 config-write seam-collapse).
type BundleResetCmd struct {
	Box      string `arg:"" optional:"" help:"Box to reset (omit to clear all)"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *BundleResetCmd) Run() error {
	return runBundleReset(c.Box, c.Instance)
}

// BundleStatusCmd is the `charly bundle status` grammar (K4-C: runs entirely plugin-side).
type BundleStatusCmd struct{}

func (c *BundleStatusCmd) Run() error {
	return runBundleStatus()
}

// BundlePathCmd is the `charly bundle path` grammar. It resolves the per-host deploy-overlay
// path entirely plugin-side (kit.DefaultDeployConfigPath — the SAME resolver core's
// DeployConfigPath aliases, R3), so it needs no host seam.
type BundlePathCmd struct{}

func (c *BundlePathCmd) Run() error {
	path, err := kit.DefaultDeployConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
