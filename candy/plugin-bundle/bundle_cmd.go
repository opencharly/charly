package bundle

import (
	"fmt"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// bundle_cmd.go — the command:bundle CLI GRAMMAR (P13). The `charly bundle …` Kong tree
// moved OUT of charly core into this plugin candy; the deploy ORCHESTRATION stayed core
// behind the deploy-add / deploy-del / deploy-from-box / deploy-config host-build seams
// (mirroring how the box-build engine stayed core behind HostBuild("image") in P8 and the
// VM-disk engine behind HostBuild("vm-build") in P10). Every leaf here is THIN: it carries
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
	AddCandy []string `long:"add-candy" help:"Extra candy to apply on top of the base image (repeatable)"`

	// Plan-level flags.
	Tag      string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	DryRun   bool   `long:"dry-run" help:"Print the plan without executing"`
	NodeOnly bool   `long:"node-only" help:"Dispatch only the named node; do not descend into nested children (children of a pod can't deploy until the pod is started)"`
	Format   string `long:"format" default:"table" enum:"table,json" help:"Output format for --dry-run"`
	Pull     bool   `long:"pull" help:"Force re-fetch of remote refs / image pull"`
	Verify   bool   `long:"verify" help:"Re-run candy tests: on the host after install"`

	// Host-only gates.
	WithServices     bool   `long:"with-services" help:"Install systemd services (host target only)"`
	AllowRepoChanges bool   `long:"allow-repo-changes" help:"Allow repo config mutations (host target only)"`
	AllowRootTasks   bool   `long:"allow-root-tasks" help:"Allow arbitrary root cmd: tasks (host target only)"`
	SkipIncompatible bool   `long:"skip-incompatible" help:"Skip candies without host-matching format (host target only)"`
	BuilderImage     string `long:"builder-image" help:"Override the compile builder image"`
	AssumeYes        bool   `long:"yes" short:"y" help:"Assume yes; implies all allow-* gates plus skip sudo preflight"`

	// Disposable + lifecycle classification (see /charly-internals:disposable).
	Disposable bool   `long:"disposable" help:"Mark this deploy disposable (authorizes autonomous charly update; writes disposable: true into charly.yml)"`
	Lifecycle  string `long:"lifecycle" help:"Informational tier tag (scratch|dev|test|qa|staging|prod|custom). NO effect on disposability — use --disposable for that."`
}

func (c *BundleAddCmd) Run() error {
	return hostDeploySeam("deploy-add", spec.DeployAddRequest{
		Name:             c.Name,
		Ref:              c.Ref,
		AddCandy:         c.AddCandy,
		Tag:              c.Tag,
		DryRun:           c.DryRun,
		NodeOnly:         c.NodeOnly,
		Format:           c.Format,
		Pull:             c.Pull,
		Verify:           c.Verify,
		WithServices:     c.WithServices,
		AllowRepoChanges: c.AllowRepoChanges,
		AllowRootTasks:   c.AllowRootTasks,
		SkipIncompatible: c.SkipIncompatible,
		BuilderImage:     c.BuilderImage,
		AssumeYes:        c.AssumeYes,
		Disposable:       c.Disposable,
		Lifecycle:        c.Lifecycle,
	})
}

// BundleDelCmd is the `charly bundle del <name>` grammar; it forwards to the deploy-del
// host-build seam. The AssumeYes field renders as `--assume-yes` (Kong derives the long
// name from the FIELD; the `long:"yes"` tag is a no-op in the separate-tag form) with `-y`
// as the short form — the exact contract charly/bundle_add_cmd.go::deployDelArgv relies on.
type BundleDelCmd struct {
	Name string `arg:"" help:"Deploy name (literal 'host' or a container deploy name)"`

	AssumeYes       bool `long:"yes" short:"y" help:"Skip confirmation prompts"`
	KeepRepoChanges bool `long:"keep-repo-changes" help:"Don't revert repo config even at zero refcount"`
	KeepServices    bool `long:"keep-services" help:"Don't disable systemd units (just stop tracking)"`
	KeepImage       bool `long:"keep-image" help:"Don't remove the synthesized overlay image (container target only)"`
	DryRun          bool `long:"dry-run" help:"Print the teardown plan without executing"`
}

func (c *BundleDelCmd) Run() error {
	return hostDeploySeam("deploy-del", spec.DeployDelRequest{
		Name:            c.Name,
		AssumeYes:       c.AssumeYes,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		KeepImage:       c.KeepImage,
		DryRun:          c.DryRun,
	})
}

// BundleFromBoxCmd is the `charly bundle from-box <ref> [name]` grammar; it forwards to the
// deploy-from-box host-build seam (a source-less deploy from an image's baked OCI labels).
type BundleFromBoxCmd struct {
	Ref       string   `arg:"" help:"Full image ref (local or registry), e.g. ghcr.io/opencharly/selkies-kde-nvidia:latest"`
	Name      string   `arg:"" optional:"" help:"Deploy name (default: the image-ref basename without tag)"`
	Instance  string   `short:"i" long:"instance" help:"Instance name"`
	Env       []string `short:"e" long:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	Port      []string `short:"p" help:"Remap host port (newHost:containerPort)"`
	Cluster   string   `long:"cluster" help:"Target a K8s cluster profile instead of a local pod (emits Kustomize via the K8s from-box path)"`
	Namespace string   `long:"namespace" help:"K8s namespace override (--cluster only)"`
}

func (c *BundleFromBoxCmd) Run() error {
	return hostDeploySeam("deploy-from-box", spec.DeployFromBoxRequest{
		Ref:       c.Ref,
		Name:      c.Name,
		Instance:  c.Instance,
		Env:       c.Env,
		Port:      c.Port,
		Cluster:   c.Cluster,
		Namespace: c.Namespace,
	})
}

// BundleShowCmd is the `charly bundle show [box]` grammar (K4-C: runs entirely plugin-side —
// deploykit.LoadBundleConfig/DeployKey are already sdk-portable, no seam needed).
type BundleShowCmd struct {
	Box      string `arg:"" optional:"" help:"Show overrides for a specific box"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BundleShowCmd) Run() error {
	return runBundleShow(c.Box, c.Instance)
}

// BundleExportCmd is the `charly bundle export [boxes…]` grammar (K4-C: runs plugin-side;
// --all reaches the project via the established HostBuild("resolved-project") seam).
type BundleExportCmd struct {
	Boxes  []string `arg:"" optional:"" help:"Boxes to export (default: all with overrides)"`
	Output string   `short:"o" help:"Write to file instead of stdout"`
	All    bool     `help:"Export all enabled boxes with all runtime fields"`
}

func (c *BundleExportCmd) Run() error {
	return runBundleExport(c.Boxes, c.Output, c.All)
}

// BundleImportCmd is the `charly bundle import <files…>` grammar (K4-C: runs plugin-side;
// the SAVE step alone reaches the host via the narrow HostBuild("deploy-config-save") seam).
type BundleImportCmd struct {
	Files   []string `arg:"" help:"Deploy YAML files to import (merged left-to-right)"`
	Replace bool     `help:"Replace entire charly.yml instead of merging with existing"`
	Box     string   `long:"box" help:"Import only this box's config"`
}

func (c *BundleImportCmd) Run() error {
	return runBundleImport(c.Files, c.Replace, c.Box)
}

// BundleResetCmd is the `charly bundle reset [box]` grammar (K4-C: runs plugin-side; the SAVE
// step alone reaches the host via the narrow HostBuild("deploy-config-save") seam).
type BundleResetCmd struct {
	Box      string `arg:"" optional:"" help:"Box to reset (omit to clear all)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
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
