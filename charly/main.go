package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk/spec"
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
	Host string `long:"host" env:"CHARLY_HOST" help:"Remote host (alias or user@host[:port]) to run this command on via SSH"`

	// Dir is the project directory that every build-mode command resolves
	// charly.yml / candy/ relative to. Default is the process
	// cwd. Useful for MCP servers and remote agents that run outside a
	// project checkout — set CHARLY_PROJECT_DIR or pass -C / --dir to point at
	// a mounted project root. Build-mode commands call os.Getwd()
	// unconditionally; when this flag is set, main() chdirs before Kong's
	// ctx.Run() so every existing call site picks up the change.
	Dir string `short:"C" long:"dir" env:"CHARLY_PROJECT_DIR" help:"Project directory containing charly.yml (default: cwd)" type:"path"`

	// Repo points charly at a remote git repo as the project source instead
	// of cwd / --dir. Spec is OWNER/REPO[@REF] (auto-prefixed with
	// github.com/) or HOST/OWNER/REPO[@REF]. The literal "default" expands
	// to opencharly/charly. main() resolves this to a local cache path
	// (~/.cache/charly/repos/...) and falls through into the existing --dir
	// chdir block, so every os.Getwd() site Just Works. Mutually exclusive
	// with --dir.
	Repo string `long:"repo" env:"CHARLY_PROJECT_REPO" placeholder:"OWNER/REPO[@REF]" help:"Read charly.yml from a remote git repo (e.g. opencharly/charly). Use 'default' for opencharly/charly."`

	Box      BoxCmd            `cmd:"" name:"box" help:"Build, generate, inspect, and pull container boxes (reads charly.yml)"`
	Plugin   PluginInternalCmd `cmd:"" name:"__plugin" hidden:"" help:"internal: plugin server/relay plumbing"`
	CliModel CliModelCmd       `cmd:"" name:"__cli-model" hidden:"" help:"internal: emit the CLI command tree as JSON (sdk.CLIModel) for the out-of-process MCP bridge"`

	// __plugin-providers prints a candy's plugin.providers (one <class>:<word> per line) —
	// the single source the PKGBUILD uses to bake the host /usr/lib/charly/plugins/.providers
	// manifest from the candy declaration (so the CLI-served command:secrets word, absent from
	// the gRPC Describe, is not missed). Reuses collectPluginProviders (R3).
	PluginProviders PluginProvidersCmd `cmd:"" name:"__plugin-providers" hidden:"" help:"internal: print a candy's plugin.providers (one <class>:<word> per line)"`

	// __preempt-status / __preempt-restore expose the resource arbiter to the externalized
	// `charly preempt …` COMMAND plugin (candy/plugin-preempt). Since cutover C9 the arbiter LOGIC
	// lives IN that plugin (verb:arbiter, compiled-in); these hidden verbs reach it through the
	// in-core PROXY (preempt.go newResourceArbiter, which dispatches to verb:arbiter over the
	// in-proc HostArbiter reverse channel). The plugin's `charly preempt` CLI shells back to these
	// sanctioned hidden verbs (the SAME __cli-model / __plugin-providers internal-command pattern),
	// so the operator-facing `charly preempt status`/`restore` CLI is byte-identical while the
	// arbiter — the last core subsystem to externalize — now lives out of the core binary.
	PreemptStatus  PreemptStatusInternalCmd  `cmd:"" name:"__preempt-status" hidden:"" help:"internal: print active resource-arbitration leases (the externalized charly preempt plugin shells back here)"`
	PreemptRestore PreemptRestoreInternalCmd `cmd:"" name:"__preempt-restore" hidden:"" help:"internal: recover preempted holders (the externalized charly preempt plugin shells back here)"`

	// __feature-list / __feature-pending / __feature-validate expose the in-core plan-description
	// machinery (LoadConfig / ScanCandy — the deepest loader — the Step plan model, and
	// validatePlanSteps, which STAYS core: it is SHARED with `charly box validate`, R3) to the
	// externalized `charly feature …` COMMAND plugin (candy/plugin-feature). The plugin shells
	// back to these sanctioned hidden verbs (the SAME __cli-model / __plugin-providers /
	// __preempt-status internal-command pattern) so the operator-facing `charly feature
	// list`/`pending`/`validate` CLI is unchanged while its implementation moved out of core.
	FeatureList     FeatureListInternalCmd     `cmd:"" name:"__feature-list" hidden:"" help:"internal: enumerate every kind: entity + its plan (the externalized charly feature plugin shells back here)"`
	FeaturePending  FeaturePendingInternalCmd  `cmd:"" name:"__feature-pending" hidden:"" help:"internal: list agent-graded plan steps (the externalized charly feature plugin shells back here)"`
	FeatureValidate FeatureValidateInternalCmd `cmd:"" name:"__feature-validate" hidden:"" help:"internal: parse + binding-consistency check for plan: blocks (the externalized charly feature plugin shells back here)"`

	// __vm re-homes the WHOLE in-core VmCmd tree (vm.go / vm_snapshot_cmd.go — build/create/
	// start/stop/destroy/console/ssh/snapshot/gpu/import/clone/cp-box/list) onto ONE hidden
	// command, exposing it to the externalized `charly vm …` COMMAND plugin (candy/plugin-vm,
	// command:vm). The VmCmd Run handlers STAY core — they drive the libvirt/qemu backends,
	// cloud-init, the VM deploy target, and the VmOp resolution RPCs the plugin's verb:libvirt
	// serves — so they cannot move to the plugin (R3); the plugin is a THIN forwarder that
	// syscall.Exec's `charly __vm <args…>` (command.go), inheriting charly's stdio/TTY so
	// `charly vm console` / `charly vm ssh` keep their interactive terminal. This is the SAME
	// internal-command pattern as __preempt-status / __feature-list, but re-homing the whole
	// subtree onto one hidden command instead of one hidden command per leaf (the VmCmd grammar
	// is large + nested, so the plugin forwards raw args rather than re-expressing each leaf).
	VmInternal VmCmd `cmd:"" name:"__vm" hidden:"" help:"internal: the VM lifecycle command tree (the externalized charly vm plugin forwards here)"`

	// __doctor re-homes the in-core DoctorCmd (doctor.go — host dependency status) onto ONE
	// hidden command, exposing it to the externalized `charly doctor` COMMAND plugin
	// (candy/plugin-doctor, command:doctor). DoctorCmd's Run handler STAYS core — it calls
	// package-main host-detection symbols (credentialHealth; DetectGPU / DetectAMDGPU /
	// GPURunArgs / DetectHostDevices in devices.go; DetectVFIO / VfioGroupAccessible /
	// MemlockLimitBytes in gpu/vfio) that this out-of-process plugin cannot reach (R3) — so the
	// plugin is a THIN forwarder that syscall.Exec's `charly __doctor <args…>` (command.go). This
	// is the SAME internal-command pattern as __vm / __feature-list / __preempt-status, re-homing
	// the leaf onto one hidden command (doctor is a flags-only leaf, so the plugin raw-forwards).
	DoctorInternal DoctorCmd `cmd:"" name:"__doctor" hidden:"" help:"internal: host dependency status (the externalized charly doctor command plugin forwards here)"`

	// vm + doctor are the last two WELDED machinery commands still re-homed onto hidden core commands
	// (__vm / __doctor above): their Run handlers STAY core — VmCmd drives the libvirt/qemu backends +
	// cloud-init + the VmOp RPCs; DoctorCmd calls the host-detection symbols (DetectGPU/DetectVFIO/
	// credentialHealth) — machinery an out-of-process plugin cannot reach (R3) — so each is a THIN
	// forwarder to `charly __<word>`. clean + settings + candy, by contrast, are now COMPILED-IN and OWN
	// their commands: clean + settings reach a shared core engine over a generic HostBuild seam (clean →
	// "retention" over charly/retention.go; settings → "settings" over the config subsystem); candy owns
	// its ENTIRE logic — the yaml.Node candy-manifest mutation — itself, sharing only the generic
	// kit.SetByDotPath / kit.MappingChild utilities (sdk/kit/yaml.go, also used by `charly box set` /
	// `box scaffold`). None of the three uses a hidden core command. NOTE: `charly version` is a DELIBERATE value/risk EXCEPTION
	// kept core (the Version field below) — NOT an "unfixable" one. RDD (2026-07-01) refuted the old
	// chicken-and-egg claim: pkgver()'s `bin/charly version` is only a convenience (the CalVer is
	// already Taskfile-computed via pkg/arch/calver.sh, and reading it from a sidecar / recomputing at
	// the superproject root sidesteps the submodule mismatch), so externalizing IS feasible. It is
	// excluded because it sheds ZERO deps, removes ~5 core lines, and would make R9's canonical identity
	// command depend on the plugin-resolution subsystem across 3 package repos — worst-value, highest-
	// blast-radius of any externalization. Operator-decided to keep core.

	// Every non-machinery command — the deploy-lifecycle + leaf-domain set (alias,
	// ssh, start, stop, status, restart, update, remove, logs,
	// shell, cmd, cp, volume, service, config, bundle, reap-orphans) PLUS check
	// — is no longer a hardcoded field: each arrives via cli.Plugins as a builtin
	// CommandProvider in its own plugin_command_<name>.go (collectCommandPlugins()).
	// (mcp/secrets/udev/tmux/preempt/feature/vm/doctor AND clean/settings/candy AND migrate are now
	// EXTERNAL commands served by candy/plugin-* , dispatched via syscall.Exec (out-of-process) or an
	// in-proc command:<word> Invoke (compiled-in); see collectExternalCommandPlugins. vm/doctor forward
	// to the hidden __vm / __doctor core commands above — their Run handlers stay core. migrate (M15) AND
	// clean AND settings AND candy OWN their engine/command in candy/plugin-migrate / candy/plugin-clean /
	// candy/plugin-settings / candy/plugin-candy (none uses a hidden core command), compiled-in so
	// command:migrate / command:clean / command:settings / command:candy resolve at init()
	// independent of any config — migrate must run when the config is exactly what cannot load.)
	// KongCommand() returns the existing <Name>Cmd struct verbatim, so the Run handler (and
	// the core machinery it calls) is unchanged: only the CLI registration LOCATION moved.
	// check is special-cased: its nested out-of-process command plugins (charly check
	// kube/adb/appium) are injected into the holder's CheckCmd.Plugins by
	// attachNestedCheckPlugins below. Only the machinery commands box / __plugin / migrate / version
	// (plus the hidden __* internals above) stay hardcoded on the CLI struct. (version stays core as a
	// deliberate value/risk EXCEPTION — NOT unfixable: RDD 2026-07-01 proved externalizing is feasible
	// but zero-value + highest-blast-radius, weakening R9's canonical identity command; operator-decided
	// to keep core. See the NOTE above the __* internals.)
	Version VersionCmd `cmd:"" help:"Print computed CalVer tag"`
}

// GenerateCmd generates Containerfiles
type GenerateCmd struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to generate (default: all enabled). The sentinel 'all' is equivalent to passing no argument."`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	IncludeDisabled bool     `long:"include-disabled" help:"Generate boxes with enabled: false in charly.yml (does not modify the file). Scoped to the named boxes when any are given."`
}

func (c *GenerateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Route generation through the compiled-in build:generate plugin over the F10 HostBuild
	// seam: the heavy Generator engine STAYS host-side in-proc (runBoxGenerate) — only this
	// BuildRequest envelope crosses. The `all` sentinel collapses to "every enabled box", and a
	// named selection scopes the resolved set (and, with --include-disabled, relaxes the gate
	// for exactly those names). See dispatchBuild + hostBuildGenerate.
	return dispatchBoxGenerate(spec.BuildRequest{
		Boxes:           normalizeBoxArgs(c.Boxes),
		Tag:             c.Tag,
		Dir:             dir,
		IncludeDisabled: c.IncludeDisabled,
	})
}

// ValidateCmd validates charly.yml and candies
type ValidateCmd struct {
	IncludeDisabled bool `long:"include-disabled" help:"Include boxes with enabled: false in validation (does not modify charly.yml)"`
}

func (c *ValidateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Load default build config for RegisterBuildVocabulary + init detection before candy scanning.
	var defaultInitCfg *InitConfig
	{
		distroCfg, _, initCfg, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			return fmt.Errorf("loading default build config: %w", err)
		}
		RegisterBuildVocabulary(distroCfg)
		defaultInitCfg = initCfg
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Populate init systems on candies from the embedded build vocabulary
	PopulateCandyInitSystem(layers, defaultInitCfg)

	return Validate(cfg, layers, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Box             string `arg:"" help:"Box name"`
	Format          string `long:"format" help:"Output a single field instead of full JSON"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	IncludeDisabled bool   `long:"include-disabled" help:"Operate on boxes with enabled: false (does not modify charly.yml)"`
}

func (c *InspectCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}
	return c.runFromConfig(cfg, dir)
}

func (c *InspectCmd) runFromConfig(cfg *Config, dir string) error {
	calverTag := ComputeCalVer()
	resolved, err := cfg.ResolveBox(c.Box, calverTag, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
	if err != nil {
		return err
	}

	if c.Format != "" {
		switch c.Format {
		case "tag":
			formatTag(resolved)
		case "base":
			formatBase(resolved)
		case "builder":
			formatBuilder(resolved)
		case "builds":
			formatBuilds(resolved)
		case "build":
			formatBuild(resolved)
		case "distro":
			formatDistro(resolved)
		case "pkg":
			formatPkg(resolved)
		case "registry":
			formatRegistry(resolved)
		case "platforms":
			formatPlatforms(resolved)
		case "candy":
			formatCandy(resolved)
		case "ports":
			return c.formatPorts(cfg, dir)
		case "volumes":
			return c.formatVolumes(cfg, dir, resolved)
		case "aliases":
			return c.formatAliases(cfg, dir)
		case "tunnel":
			c.formatTunnel(cfg, dir, resolved)
		case "network":
			fmt.Println(resolved.Network)
		case "engine":
			layers, err := ScanAllCandyWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			engine := ResolveBoxEngine(cfg, layers, c.Box, "")
			if engine == "" {
				engine = "(global default)"
			}
			fmt.Println(engine)
		case "bind_mounts":
			// bind_mounts are now deploy-time only; show charly.yml volume config
			if overlay, ok := loadDeployConfigForRead("charly box inspect bind_mounts").Lookup(c.Box, c.Instance); ok {
				for _, dv := range overlay.Volume {
					fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
				}
			}
		case "version":
			fmt.Println(resolved.Version)
		case "status":
			fmt.Println(resolveStatus(resolved.Status))
		case "info":
			fmt.Println(resolved.Info)
		default:
			return fmt.Errorf("unknown format field: %s", c.Format)
		}
		return nil
	}

	data, err := json.MarshalIndent(resolved, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// formatTag prints the resolved box's full tag.
func formatTag(resolved *ResolvedBox) { fmt.Println(resolved.FullTag) }

// formatBase prints the resolved box's base image.
func formatBase(resolved *ResolvedBox) { fmt.Println(resolved.Base) }

// formatBuilder prints the resolved builder map (type: image).
func formatBuilder(resolved *ResolvedBox) {
	for typ, builder := range resolved.Builder {
		fmt.Printf("%s: %s\n", typ, builder)
	}
}

// formatBuilds prints the resolved builder capabilities.
func formatBuilds(resolved *ResolvedBox) {
	for _, b := range resolved.BuilderCapabilities {
		fmt.Println(b)
	}
}

// formatBuild prints the resolved build formats.
func formatBuild(resolved *ResolvedBox) {
	for _, b := range resolved.BuildFormats {
		fmt.Println(b)
	}
}

// formatDistro prints the resolved distro chain.
func formatDistro(resolved *ResolvedBox) {
	for _, d := range resolved.Distro {
		fmt.Println(d)
	}
}

// formatPkg prints the resolved package manager.
func formatPkg(resolved *ResolvedBox) { fmt.Println(resolved.Pkg) }

// formatRegistry prints the resolved registry.
func formatRegistry(resolved *ResolvedBox) { fmt.Println(resolved.Registry) }

// formatPlatforms prints the resolved platforms.
func formatPlatforms(resolved *ResolvedBox) {
	for _, p := range resolved.Platforms {
		fmt.Println(p)
	}
}

// formatCandy prints the resolved candy list.
func formatCandy(resolved *ResolvedBox) {
	for _, l := range resolved.Candy {
		fmt.Println(l)
	}
}

// formatPorts prints the collected box ports.
func (c *InspectCmd) formatPorts(cfg *Config, dir string) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	ports, err := CollectBoxPorts(cfg, layers, c.Box)
	if err != nil {
		return err
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return nil
}

// formatVolumes prints the collected box volumes.
func (c *InspectCmd) formatVolumes(cfg *Config, dir string, resolved *ResolvedBox) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	volumes, err := CollectBoxVolume(cfg, layers, c.Box, resolved.Home, nil)
	if err != nil {
		return err
	}
	for _, vol := range volumes {
		fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
	}
	return nil
}

// formatAliases prints the collected box aliases.
func (c *InspectCmd) formatAliases(cfg *Config, dir string) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	aliases, err := CollectBoxAlias(cfg, layers, c.Box)
	if err != nil {
		return err
	}
	for _, a := range aliases {
		fmt.Printf("%s\t%s\n", a.Name, a.Command)
	}
	return nil
}

// formatTunnel prints the deploy-time tunnel config for the box. Schema v4:
// Tunnel moved off BoxConfig/ResolvedBox — deploy-only. Resolve from
// BundleNode.Tunnel via charly.yml. Any resolution failure is silently
// skipped (no tunnel output), matching the prior inline behaviour.
func (c *InspectCmd) formatTunnel(cfg *Config, dir string, resolved *ResolvedBox) {
	overlay, ok := loadDeployConfigForRead("charly box inspect tunnel").Lookup(c.Box, c.Instance)
	if !ok || overlay.Tunnel == nil {
		return
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return
	}
	portProtos := make(map[int]string)
	boxPorts, _ := CollectBoxPorts(cfg, layers, c.Box)
	tc := ResolveTunnelConfig(overlay.Tunnel, c.Box, "", layers, resolved.Candy, portProtos, boxPorts)
	if tc == nil || len(tc.Ports) == 0 {
		return
	}
	fmt.Println("PORT\tACCESS\tPROTOCOL\tHOSTNAME")
	for _, tp := range tc.Ports {
		access := "private"
		if tp.Public {
			access = "public"
		}
		hostname := tp.Hostname
		if hostname == "" {
			hostname = "-"
		}
		fmt.Printf("%d\t%s\t%s\t%s\n", tp.Port, access, tp.Protocol, hostname)
	}
}

// ListCmd groups list subcommands
type ListCmd struct {
	Aliases  ListAliasesCmd  `cmd:"" help:"List candies that declare aliases"`
	Boxes    ListBoxesCmd    `cmd:"" name:"boxes" help:"List boxes from charly.yml"`
	Candies  ListCandiesCmd  `cmd:"" name:"candies" help:"List candies from the filesystem"`
	Routes   ListRoutesCmd   `cmd:"" help:"List candies that declare a route"`
	Services ListServicesCmd `cmd:"" help:"List candies that declare a service"`
	Targets  ListTargetsCmd  `cmd:"" help:"List build targets in dependency order"`
	Volumes  ListVolumesCmd  `cmd:"" help:"List candies that declare volumes"`
	Tags     ListTagsCmd     `cmd:"" help:"List locally stored CalVer tags of charly-built images, newest first (rollback discovery for charly update --tag)"`
}

// ListBoxesCmd lists boxes from charly.yml
type ListBoxesCmd struct{}

func (c *ListBoxesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if errors.Is(err, ErrNoCharlyYml) {
		// No project in this dir → zero boxes. An empty list is not an error (like
		// `ls` in an empty dir), so `box list boxes` exits 0 with no output. This is
		// the coder-pod / fresh-workspace case: the charly-mcp box.list.boxes tool
		// runs in CHARLY_PROJECT_DIR (/workspace) before any charly.yml exists.
		return nil
	}
	if err != nil {
		return err
	}

	for _, name := range cfg.BoxNames() {
		_ = cfg.Box[name]
		// Boxes author no status; the effective rung (worst of the candy chain)
		// is computed for the ai.opencharly.status label at generate time.
		status := resolveStatus("")
		if status != "working" {
			fmt.Printf("%s [%s]\n", name, status)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListCandiesCmd lists candies from filesystem
type ListCandiesCmd struct{}

func (c *ListCandiesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	for _, name := range CandyNames(layers) {
		layer := layers[name]
		status := candyStatus(layer)
		var tags []string
		if layer.Remote {
			tags = append(tags, layer.RepoPath)
		}
		if status != "working" {
			tags = append(tags, status)
		}
		if len(tags) > 0 {
			fmt.Printf("%s [%s]\n", name, strings.Join(tags, ", "))
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListTargetsCmd lists build targets in dependency order
type ListTargetsCmd struct{}

func (c *ListTargetsCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	calverTag := ComputeCalVer()
	images, err := cfg.ResolveAllBox(calverTag, dir, ResolveOpts{})
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Compute intermediates to get full build order
	images, err = ComputeIntermediates(images, layers, cfg, calverTag)
	if err != nil {
		return err
	}

	order, err := ResolveBoxOrder(images, layers)
	if err != nil {
		return err
	}

	for _, name := range order {
		img := images[name]
		if img.Auto {
			fmt.Printf("%s [auto]\n", name)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListServicesCmd lists candies that trigger any init system
type ListServicesCmd struct{}

func (c *ListServicesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	services := InitCandy(layers)
	for _, layer := range services {
		fmt.Println(layer.Name)
	}
	return nil
}

// ListRoutesCmd lists candies with route files
type ListRoutesCmd struct{}

func (c *ListRoutesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	routes := RouteCandy(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(routes))
	for _, layer := range routes {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		route, err := layer.Route()
		if err != nil {
			return err
		}
		fmt.Printf("%s\thost=%s\tport=%s\n", name, route.Host, route.Port)
	}
	return nil
}

// ListVolumesCmd lists candies with volume declarations
type ListVolumesCmd struct{}

func (c *ListVolumesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	vols := VolumeCandy(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(vols))
	for _, layer := range vols {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, vol := range layer.Volume() {
			fmt.Printf("%s\t%s\t%s\n", name, vol.Name, vol.Path)
		}
	}
	return nil
}

// NewCmd groups scaffolding subcommands
type NewCmd struct {
	Candy   NewCandyCmd   `cmd:"" name:"candy" help:"Scaffold a candy directory"`
	Project NewProjectCmd `cmd:"" help:"Scaffold a fresh charly project (charly.yml + candy/)"`
	Box     NewBoxCmd     `cmd:"" name:"box" help:"Add a new box entry to charly.yml"`
}

// NewCandyCmd scaffolds a new candy
type NewCandyCmd struct {
	Name string `arg:"" help:"Candy name"`
}

func (c *NewCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return ScaffoldCandy(dir, c.Name)
}

// VersionCmd prints the computed CalVer tag
type VersionCmd struct{}

func (c *VersionCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	// The BINARY's identity (stamped at build time), NOT the wall clock.
	fmt.Println(CharlyVersion())
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
		if err := LoadProcessDotenv(dir); err != nil {
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
	// `charly check` is now itself a builtin command provider; inject its nested
	// out-of-process subcommands (charly check kube/adb/appium) into the holder's
	// CheckCmd.Plugins — the wiring that previously read cli.Check.Plugins directly.
	attachNestedCheckPlugins(cmdPlugins, nestedCmds["check"])
	cli.Plugins = append(cmdPlugins, topCmds...)
	ctx := kong.Parse(&cli,
		kong.Name("charly"),
		kong.Description("OpenCharly - the container management experience for you and your agents"),
		kong.UsageOnError(),
	)

	// --host: re-exec over SSH (unless we're running a LocalOnly
	// command like `settings`, `version`, or `ssh tunnel`). Doing
	// this AFTER Kong parse ensures --help / invalid-flag cases print
	// locally; doing it BEFORE ctx.Run() ensures no local state is
	// touched when we're about to forward the command.
	if shouldReexecForHost(&cli, ctx.Command()) {
		os.Exit(ReexecOverSSH(&cli))
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

	// Stale-binary guardrail: if cwd is inside an opencharly source tree
	// AND the source tree has .go files newer than this binary, abort
	// with a clear error pointing at `task build:charly`. See
	// CheckBinaryFreshness for the full rationale (CLAUDE.md R9 +
	// the 2026-05-09 cuda-cudnn cache-mount incident).
	CheckBinaryFreshness(ctx.Command())

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
	RegisterShutdownHook(reapPlugins)
	InstallSignalHandler()
	SweepStaleTemps()
	defer reapPlugins()

	// A dynamic command plugin's command has no Run() method, so dispatch it manually:
	// dispatchCommand routes by placement — a COMPILED-IN command candy in-proc via Invoke(OpRun),
	// an OUT-OF-PROCESS one by syscall.Exec (F8) — with the pass-through args; everything else runs
	// through Kong's normal ctx.Run().
	var err error
	if d, ok := extCmdTable[commandPathKey(ctx.Command())]; ok {
		err = dispatchCommand(d)
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
	// failed. See CheckFailedError / CheckFailExitCode in check_cmd.go.
	if err != nil {
		if _, ok := errors.AsType[*CheckFailedError](err); ok {
			fmt.Fprintln(os.Stderr, FormatCLIError(err))
			os.Exit(CheckFailExitCode) //nolint:gocritic // reapPlugins() called explicitly at :953 before this os.Exit; the deferred reap is a redundant safety net
		}
	}
	ctx.FatalIfErrorf(FormatCLIError(err))
}
