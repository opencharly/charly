package pod

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pod_cmd.go — the pod-lifecycle CLI GRAMMAR (the DEPLOY-wave CLI-struct port). Each `charly
// <word> …` Kong tree moved OUT of charly core into this plugin candy. Cutover B unit 2
// (pod-lifecycle-CLI-dispatch): start/stop/logs/shell/update now perform their OWN validation
// (CanonicalizeDeployArg/RejectImageRefAsDeployName/remote-ref rejection — all pure sdk/deploykit +
// sdk/spec calls) HERE, in the plugin, then forward via hostPodSeam to a HostBuild seam whose host
// body is JUST the irreducible ResolveTarget + live-executor dispatch a plugin cannot hold
// (charly/host_build_pod_lifecycle_dispatch.go) — the "bodies move, shells follow" cutover. A leaf
// with NO registry coupling (restart) calls sdk/deploykit directly — no seam needed. service is
// FULLY ported too (buildServiceArgv, service_resolve.go, resolves + validates + renders the argv
// here, forwarding only the rendered argv).
//
// remove is FULLY ported too (option (b), full parity with the other 6 verbs): its WHOLE
// orchestration — tunnel-stop (remove_tunnel.go, verb:tunnel over InvokeProvider) AND the
// quadlet/container-teardown/hook/cleanup body (remove_orchestration.go's runPodRemove) — runs
// HERE now. Two axes still reach the host, each over its own EXISTING narrow seam (no new
// mechanism, R3): the credential-backed hook env (pod-config-hook-secret-env) and the deploy-entry
// cleanup's registry-resugar (the NEW pod-config-clean-deploy-entry, a narrow twin of
// pod-config-save-deploy-state — the existing deploy-config-save seam's shape doesn't fit, see
// remove_orchestration.go's header for the demonstrated mismatch). The arbiter-release bracket
// (CHARLY_PREEMPT_LEASE-gated host-process state) stays under the EXISTING "pod-remove" HostBuild
// kind, deferred here as the LAST step — same shape as pod start/stop's own bracket.
//
// RDD caught a real latent placement bug mid-port (see remove_orchestration.go's header): two
// deploykit calls that "looked" portable (resolveSidecarNames' LoadBundleConfig,
// runPodRemove's ResolveBoxEngineForDeploy) transitively depend on deploykit.DeployStateHost,
// which only charly-core's own init() populates — so both were rerouted through their own
// EXISTING seams (pod-config-load-bundle, pod-config-box-engine) instead of calling deploykit
// directly.

// StartCmd launches a container with supervisord in the background — the `charly start` grammar.
type StartCmd struct {
	Box          string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag          string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build        bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env          []string `short:"e" long:"env" sep:"none" help:"Set container env var (direct mode only)"`
	EnvFile      string   `long:"env-file" help:"Load env vars from file (direct mode only)"`
	Instance     string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Port         []string `short:"p" help:"Remap host port (direct mode only)"`
	VolumeFlag   []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind         []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	NoAutoDetect bool     `long:"no-autodetect" help:"Disable automatic device detection"`
}

func (c *StartCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly start <image-name>'", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	if err := deploykit.RejectImageRefAsDeployName(c.Box); err != nil {
		return err
	}
	return hostPodSeam("pod-start", spec.PodStartRequest{
		Box:          c.Box,
		Tag:          c.Tag,
		Build:        c.Build,
		Env:          c.Env,
		EnvFile:      c.EnvFile,
		Instance:     c.Instance,
		Port:         c.Port,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
	})
}

// StopCmd stops a running container started by StartCmd — the `charly stop` grammar.
type StopCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Unmount  bool   `long:"unmount" help:"After stopping, also tear down encrypted FUSE mounts and gocryptfs scope units (charly-enc-<box>-<volume>.scope) for this box"`
}

func (c *StopCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// Resolve the image name (handle remote refs).
	boxName := c.Box
	ref := kit.StripURLScheme(c.Box)
	if spec.IsRemoteImageRef(ref) {
		boxName = spec.ParseRemoteRef(ref).Name
	}
	return hostPodSeam("pod-stop", spec.PodStopRequest{
		Box:      boxName,
		Instance: c.Instance,
		Unmount:  c.Unmount,
	})
}

// RestartCmd restarts a service container — the `charly restart` grammar. In quadlet mode it
// issues a single `systemctl --user restart`, which is atomic from systemd's perspective —
// ExecStopPost (e.g. tailscale serve --off) runs before ExecStartPost (tailscale serve), and the
// unit ends in either active or failed, never the silent stopped state a manual stop+start
// sequence can produce when start fails. NOT registry-bound (no ResolveTarget/plugin-loader need)
// — calls deploykit.RestartPodService directly, zero HostBuild round-trip.
type RestartCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
}

func (c *RestartCmd) Run() error {
	boxName := c.Box
	ref := kit.StripURLScheme(c.Box)
	if spec.IsRemoteImageRef(ref) {
		boxName = spec.ParseRemoteRef(ref).Name
	}
	return deploykit.RestartPodService(boxName, c.Instance)
}

// LogsCmd shows service container logs — the `charly logs` grammar. Registry-bound
// (dispatchLifecycleTarget/LifecycleTarget — core Mechanisms) — forwards via HostBuild("pod-logs").
type LogsCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Follow   bool   `short:"f" long:"follow" help:"Follow log output"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Sidecar  string `long:"sidecar" help:"Show the named SIDECAR container's logs instead of the app container's"`
}

func (c *LogsCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return hostPodSeam("pod-logs", spec.PodLogsRequest{
		Box:      c.Box,
		Follow:   c.Follow,
		Instance: c.Instance,
		Sidecar:  c.Sidecar,
	})
}

// RemoveCmd removes a service container — the `charly remove` grammar. Cutover B unit 2 remove-verb
// completion (option (b), full parity with the other 6 verbs): the plugin now owns the WHOLE
// orchestration itself — tunnel-stop (resolveContainerTunnel + podTunnelStop, remove_tunnel.go —
// verb:tunnel over InvokeProvider) and the quadlet/container-teardown/hook/cleanup body
// (runPodRemove, remove_orchestration.go), reaching the host only for the two genuinely
// host-coupled axes (the credential-backed hook env via the EXISTING pod-config-hook-secret-env
// seam, and the deploy-entry cleanup via the NEW pod-config-clean-deploy-entry seam — see
// remove_orchestration.go's header for why the existing deploy-config-save seam doesn't fit). The
// arbiter-release bracket stays host-side under the EXISTING "pod-remove" HostBuild kind — same
// shape as pod start/stop's own bracket — deferred here as the LAST step so it always runs,
// mirroring the former core `defer releaseResourceClaim(...)` semantics exactly.
type RemoveCmd struct {
	Box        string   `arg:"" help:"Box name or remote ref"`
	Instance   string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Purge      bool     `long:"purge" help:"Also remove named volumes"`
	KeepDeploy bool     `name:"keep-deploy" help:"Keep charly.yml entry for this box"`
	Env        []string `short:"e" long:"env" sep:"none" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	boxName := kit.ResolveBoxName(c.Box)
	defer func() {
		_ = hostPodSeam("pod-remove", spec.PodRemoveRequest{Box: boxName, Instance: c.Instance})
	}()

	if tc := resolveContainerTunnel(c.Box, c.Instance); tc != nil {
		if err := podTunnelStop(tc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
	return runPodRemove(c.Box, c.Instance, c.Purge, c.KeepDeploy, c.Env)
}

// ShellCmd starts a bash shell in a container image — the `charly shell` grammar. Registry-bound
// (dispatchLifecycleTarget/LifecycleTarget — core Mechanisms) — forwards via HostBuild("pod-shell").
type ShellCmd struct {
	Box          string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag          string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Command      string   `short:"c" help:"Command to execute instead of interactive shell"`
	Build        bool     `long:"build" help:"Force local build instead of pulling from registry"`
	TTY          bool     `long:"tty" help:"Force TTY allocation (for automation tools that lack a real terminal)"`
	Env          []string `short:"e" long:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	EnvFile      string   `long:"env-file" help:"Load env vars from file"`
	Instance     string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	VolumeFlag   []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind         []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	NoAutoDetect bool     `long:"no-autodetect" help:"Disable automatic device detection"`
}

func (c *ShellCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`. Users must pull
	// first, then run shell on the short name.
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly shell <image-name>'", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return hostPodSeam("pod-shell", spec.PodShellRequest{
		Box:          c.Box,
		Tag:          c.Tag,
		Command:      c.Command,
		Build:        c.Build,
		TTY:          c.TTY,
		Env:          c.Env,
		EnvFile:      c.EnvFile,
		Instance:     c.Instance,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
	})
}

// ServiceCmd manages services inside a running container — the `charly service` grammar. Cutover
// B unit 2 completion: each leaf now resolves + validates + renders the FULL argv itself
// (buildServiceArgv, service_resolve.go — all portable) and forwards it via ONE
// HostBuild("pod-service") seam whose host body does ONLY dispatchLifecycleTarget +
// LifecycleTarget.Shell (the irreducible registry-bound step).
type ServiceCmd struct {
	Restart ServiceRestartCmd `cmd:"" help:"Restart an in-container service"`
	Start   ServiceStartCmd   `cmd:"" help:"Start an in-container service"`
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of in-container services"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop an in-container service"`
}

// ServiceStatusCmd shows status of all services
type ServiceStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "status", "")
	if err != nil {
		return err
	}
	return hostPodSeam("pod-service", spec.PodServiceRequest{Box: c.Box, Instance: c.Instance, Argv: argv})
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "start", c.Service)
	if err != nil {
		return err
	}
	return hostPodSeam("pod-service", spec.PodServiceRequest{Box: c.Box, Instance: c.Instance, Argv: argv})
}

// ServiceStopCmd stops a service
type ServiceStopCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "stop", c.Service)
	if err != nil {
		return err
	}
	return hostPodSeam("pod-service", spec.PodServiceRequest{Box: c.Box, Instance: c.Instance, Argv: argv})
}

// ServiceRestartCmd restarts a service
type ServiceRestartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "restart", c.Service)
	if err != nil {
		return err
	}
	return hostPodSeam("pod-service", spec.PodServiceRequest{Box: c.Box, Instance: c.Instance, Argv: argv})
}

// VolumeCmd groups the named-volume verbs — the `charly volume` grammar. NOT registry-bound (pure
// sdk/kit + sdk/deploykit exec logic) — moves wholesale, zero seam.
type VolumeCmd struct {
	List  VolumeListCmd  `cmd:"" help:"List a deployment's charly-managed named volumes with their backing mountpoints"`
	Reset VolumeResetCmd `cmd:"" help:"Remove ONE named volume so the next start recreates it fresh (e.g. wipe a sidecar's state volume to force re-auth)"`
}

// VolumeListCmd lists the engine-side named volumes belonging to a
// deployment (app + sidecar volumes alike), with their host mountpoints —
// the charly-native replacement for ad-hoc `podman volume ls/inspect`.
type VolumeListCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VolumeListCmd) Run() error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	boxName := kit.ResolveBoxName(c.Box)
	bin := kit.EngineBinary(deploykit.ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine))
	prefix := kit.ContainerNameInstance(boxName, c.Instance) + "-"
	out, err := exec.Command(bin, "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return fmt.Errorf("listing volumes: %w", err)
	}
	var names []string
	for n := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if n != "" && strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fmt.Printf("No named volumes for %s (prefix %s)\n", boxName, prefix)
		return nil
	}
	sort.Strings(names)
	for _, n := range names {
		mp, mpErr := exec.Command(bin, "volume", "inspect", "--format", "{{.Mountpoint}}", n).Output()
		mount := strings.TrimSpace(string(mp))
		if mpErr != nil {
			mount = "(mountpoint unavailable)"
		}
		fmt.Printf("%s\t%s\n", n, mount)
	}
	return nil
}

// VolumeResetCmd removes ONE named volume so the next `charly start`
// recreates it fresh — the charly-native replacement for the retired
// `podman volume rm <name>` re-initialization path (sidecar state wipes,
// corrupted caches). The engine refuses an in-use volume, so a running
// deployment surfaces an actionable error instead of silent data loss.
type VolumeResetCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Name     string `arg:"" help:"Volume name — bare (e.g. tailscale-state) or the full charly-<box>-<name> form"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VolumeResetCmd) Run() error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	boxName := kit.ResolveBoxName(c.Box)
	bin := kit.EngineBinary(deploykit.ResolveBoxEngineForDeploy(boxName, c.Instance, rt.RunEngine))
	full := c.Name
	if !strings.HasPrefix(full, "charly-") {
		full = kit.ContainerNameInstance(boxName, c.Instance) + "-" + c.Name
	}
	if out, err := exec.Command(bin, "volume", "rm", full).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "no such volume") {
			return fmt.Errorf("volume %s does not exist", full)
		}
		return fmt.Errorf("removing volume %s: %s — an in-use volume is refused; stop the deployment first (`charly stop %s`)", full, msg, boxName)
	}
	fmt.Printf("Removed volume %s — the next `charly start %s` recreates it fresh\n", full, boxName)
	return nil
}

// CpCmd copies a file between the host and a running container (app or
// sidecar) — the charly-native replacement for ad-hoc `podman cp`. Exactly
// one of <src>/<dst> carries the ':' prefix marking the container side. NOT
// registry-bound — moves wholesale, zero seam.
type CpCmd struct {
	Box      string `arg:"" help:"Box / deploy name"`
	Src      string `arg:"" help:"Source path — prefix with ':' for the container side"`
	Dst      string `arg:"" help:"Destination path — prefix with ':' for the container side"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Sidecar  string `long:"sidecar" help:"Target the named SIDECAR container instead of the app container"`
}

// ConfigCmd groups box configuration subcommands — the `charly config` grammar. Default
// subcommand (no keyword): full setup (quadlet + secrets + enc). Every leaf's actual body is
// deeply core-type-coupled (BundleConfig/ResolvedSidecar/enc*/deploykit.CleanDeployEntry, and
// Setup is ALSO constructed directly, by its EXACT unchanged name, by bundle_from_box_cmd.go —
// P13-kernel, out of this wave's scope — so the core struct cannot rename/move), so each leaf
// forwards via its own HostBuild("pod-config-<leaf>") seam.
type ConfigCmd struct {
	Mount   ConfigMountCmd   `cmd:"mount" help:"Mount encrypted volumes"`
	Passwd  ConfigPasswdCmd  `cmd:"passwd" help:"Change gocryptfs password"`
	Remove  ConfigRemoveCmd  `cmd:"remove" help:"Remove quadlet and disable service"`
	Setup   ConfigSetupCmd   `cmd:"" default:"withargs" help:"Setup quadlet, secrets, and encrypted volumes"`
	Status  ConfigStatusCmd  `cmd:"status" help:"Show encrypted volume status"`
	Unmount ConfigUnmountCmd `cmd:"unmount" help:"Unmount encrypted volumes"`
}

// ConfigSetupCmd configures a box: generates quadlet, provisions secrets, initializes and mounts
// encrypted volumes — the `charly config [setup]` grammar (mirrors core's BoxConfigSetupCmd 1:1).
type ConfigSetupCmd struct {
	Box           string   `arg:"" optional:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag           string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build         bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env           []string `short:"e" long:"env" sep:"none" help:"Set container env var (KEY=VALUE), merged with existing vars"`
	Clean         bool     `short:"c" long:"clean" help:"Replace all env vars instead of merging (clean slate)"`
	EnvFile       string   `long:"env-file" help:"Load env vars from file"`
	Instance      string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Port          []string `short:"p" help:"Remap host port (newHost:containerPort, e.g., 5901:5900)"`
	KeepMounted   bool     `long:"keep-mounted" help:"Keep encrypted volumes mounted after setup"`
	Password      string   `long:"password" default:"auto" enum:"auto,manual" help:"auto: generate secrets (default), manual: prompt for each"`
	RefreshSecret []string `name:"refresh-secret" help:"Force re-provisioning of the named podman secret(s) from their source on this run ('all' = every secret of this image, sidecars included): the charly-<image>-<name> secret is removed and recreated. A candy-owned auto-generated secret gets a NEW value — re-initialize services that stored the old one"`
	VolumeFlag    []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path]). Type: volume|bind|encrypted"`
	Bind          []string `long:"bind" help:"Shorthand: configure volume as bind mount (name or name=path)"`
	Encrypt       []string `long:"encrypt" help:"Shorthand: configure volume as encrypted (gocryptfs)"`
	MemoryMax     string   `long:"memory-max" help:"Cgroup memory.max hard OOM limit (e.g. 6g, 500m). Persists to charly.yml."`
	MemoryHigh    string   `long:"memory-high" help:"Cgroup memory.high soft limit — reclaim pressure before OOM. Persists to charly.yml."`
	MemorySwapMax string   `long:"memory-swap-max" help:"Cgroup memory.swap.max ceiling. Persists to charly.yml."`
	Cpus          string   `long:"cpus" help:"CPU quota in cores (e.g. 2.5 for 2.5 cores). Persists to charly.yml."`
	Seed          bool     `long:"seed" default:"true" negatable:"" help:"Seed bind-backed volumes with data from image (default: true)"`
	ForceSeed     bool     `long:"force-seed" help:"Re-seed even if target directory is not empty"`
	DataFrom      string   `long:"data-from" help:"Seed data from this data image instead of the target image"`
	UpdateAll     bool     `long:"update-all" help:"Regenerate quadlets for all other deployed boxes to pick up env_provides changes"`
	SshKey        string   `long:"ssh-key" help:"SSH public key: 'auto' (default ~/.ssh key), path to .pub file, 'generate', or 'none'"`
	Sidecar       []string `long:"sidecar" help:"Attach sidecar (from built-in templates, e.g. 'tailscale')"`
	ListSidecars  bool     `long:"list-sidecars" help:"List available sidecar templates and exit"`
	NoAutoDetect  bool     `long:"no-autodetect" help:"Disable automatic device detection"`
}

func (c *ConfigSetupCmd) Run() error {
	return hostPodSeam("pod-config-setup", spec.PodConfigSetupRequest{
		Box:           c.Box,
		Tag:           c.Tag,
		Build:         c.Build,
		Env:           c.Env,
		Clean:         c.Clean,
		EnvFile:       c.EnvFile,
		Instance:      c.Instance,
		Port:          c.Port,
		KeepMounted:   c.KeepMounted,
		Password:      c.Password,
		RefreshSecret: c.RefreshSecret,
		VolumeFlag:    c.VolumeFlag,
		Bind:          c.Bind,
		Encrypt:       c.Encrypt,
		MemoryMax:     c.MemoryMax,
		MemoryHigh:    c.MemoryHigh,
		MemorySwapMax: c.MemorySwapMax,
		Cpus:          c.Cpus,
		Seed:          c.Seed,
		ForceSeed:     c.ForceSeed,
		DataFrom:      c.DataFrom,
		UpdateAll:     c.UpdateAll,
		SshKey:        c.SshKey,
		Sidecar:       c.Sidecar,
		ListSidecars:  c.ListSidecars,
		NoAutoDetect:  c.NoAutoDetect,
	})
}

// ConfigStatusCmd shows status of all services
type ConfigStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ConfigStatusCmd) Run() error {
	return hostPodSeam("pod-config-status", spec.PodConfigStatusRequest{Box: c.Box, Instance: c.Instance})
}

// ConfigMountCmd mounts encrypted volumes.
type ConfigMountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only mount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ConfigMountCmd) Run() error {
	return hostPodSeam("pod-config-mount", spec.PodConfigMountRequest{Box: c.Box, Volume: c.Volume, Instance: c.Instance})
}

// ConfigUnmountCmd unmounts encrypted volumes.
type ConfigUnmountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only unmount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ConfigUnmountCmd) Run() error {
	return hostPodSeam("pod-config-unmount", spec.PodConfigUnmountRequest{Box: c.Box, Volume: c.Volume, Instance: c.Instance})
}

// ConfigPasswdCmd changes the gocryptfs password.
type ConfigPasswdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ConfigPasswdCmd) Run() error {
	return hostPodSeam("pod-config-passwd", spec.PodConfigPasswdRequest{Box: c.Box, Instance: c.Instance})
}

// ConfigRemoveCmd removes a quadlet service (replaces charly disable).
type ConfigRemoveCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ConfigRemoveCmd) Run() error {
	return hostPodSeam("pod-config-remove", spec.PodConfigRemoveRequest{Box: c.Box, Instance: c.Instance})
}

// UpdateCmd updates an image (pulls/builds the latest), preserves the existing deploy config
// (user-overlay state untouched), and restarts the service to pick up the new image — the
// `charly update` grammar. Registry-bound (resolveTreeRoot/loadDeployPlugins/ResolveTarget —
// core Mechanisms) — forwards via HostBuild("pod-update").
type UpdateCmd struct {
	Box       string `arg:"" help:"Deploy name (resolved via charly.yml) OR box name. For deploys, the target's update strategy is auto-selected (pod=systemctl restart with new image; vm=in-guest candy re-apply; local=idempotent re-apply)."`
	Tag       string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build     bool   `long:"build" help:"Force local build instead of pulling from registry"`
	Instance  string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Seed      bool   `long:"seed" default:"true" negatable:"" help:"Sync data from new image into bind-backed volumes (default: true)"`
	ForceSeed bool   `long:"force-seed" help:"Overwrite existing data in volumes (default: only add new files)"`
	DataFrom  string `long:"data-from" help:"Sync data from this data image instead"`
}

func (c *UpdateCmd) Run() error {
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return hostPodSeam("pod-update", spec.PodUpdateRequest{
		Box:       c.Box,
		Tag:       c.Tag,
		Build:     c.Build,
		Instance:  c.Instance,
		Seed:      c.Seed,
		ForceSeed: c.ForceSeed,
		DataFrom:  c.DataFrom,
	})
}

func (c *CpCmd) Run() error {
	srcInCtr := strings.HasPrefix(c.Src, ":")
	dstInCtr := strings.HasPrefix(c.Dst, ":")
	if srcInCtr == dstInCtr {
		return fmt.Errorf("exactly one of <src>/<dst> must carry the ':' container-side prefix (got src=%q dst=%q)", c.Src, c.Dst)
	}
	var engine, name string
	var err error
	if c.Sidecar != "" {
		engine, name, err = deploykit.ResolveSidecarContainer(c.Box, c.Instance, c.Sidecar)
	} else {
		engine, name, err = deploykit.ResolveContainer(c.Box, c.Instance)
	}
	if err != nil {
		return err
	}
	src, dst := c.Src, c.Dst
	if srcInCtr {
		src = name + ":" + strings.TrimPrefix(src, ":")
	} else {
		dst = name + ":" + strings.TrimPrefix(dst, ":")
	}
	cmd := exec.Command(engine, "cp", src, dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s cp %s %s: %w", engine, src, dst, err)
	}
	return nil
}
