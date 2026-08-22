package pod

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/fleet"
	"github.com/opencharly/spec/spec"
)

// pod_cmd.go — the pod-lifecycle CLI GRAMMAR (the DEPLOY-wave CLI-struct port). Each `charly
// <word> …` Kong tree moved OUT of charly core into this plugin candy. Cutover B unit 2
// (pod-lifecycle-CLI-dispatch): start/stop/logs/shell/update now perform their OWN validation
// (CanonicalizeDeployArg/RejectImageRefAsDeployName/remote-ref rejection — all pure sdk/deploykit +
// sdk/spec calls) HERE, in the plugin, then forward via hostPodLifecycle (host_seams.go — #55 W3
// A10b's ONE op-discriminated "pod-lifecycle" wire request, replacing the former 8 dedicated
// per-verb hostPodSeam calls) to a HostBuild seam whose host body is JUST the irreducible
// ResolveTarget + live-executor dispatch a plugin cannot hold
// (charly/host_build_pod_lifecycle_dispatch.go) — the "bodies move, shells follow" cutover. A leaf
// with NO registry coupling (restart) calls sdk/deploykit directly — no seam needed. service is
// FULLY ported too (buildServiceArgv, service_resolve.go, resolves + validates + renders the argv
// here, forwarding only the rendered argv).
//
// remove is FULLY ported too (option (b), full parity with the other 6 verbs): its WHOLE
// orchestration — tunnel-stop (remove_tunnel.go, verb:tunnel over InvokeProvider) AND the
// quadlet/container-teardown/hook/cleanup body (remove_orchestration.go's runPodRemove) — runs
// HERE now. The two former host-coupled axes are BOTH retired: the credential-backed hook env is
// resolved plugin-side (deploykit.ResolveHookSecretEnv + verb:credential, the retired
// pod-config-hook-secret-env seam) and the deploy-entry cleanup runs plugin-side
// (deploykit.CleanDeployEntry via loader-threaded Primaries, the deleted pod-config-clean-deploy-entry
// seam). The arbiter-release bracket (CHARLY_PREEMPT_LEASE-gated host-process state) stays under
// the "pod-lifecycle" HostBuild op="remove" (#55 W3 A10b unified the former dedicated "pod-remove"
// kind into this one), deferred here as the LAST step — same shape as pod start/stop's own bracket.
//
// RDD caught a real latent placement bug mid-port (see remove_orchestration.go's header): two
// deploykit calls that "looked" portable (resolveSidecarNames' LoadFleetConfig,
// runPodRemove's ResolveBoxEngineForDeploy) transitively depend on deploykit.DeployStateHost,
// which only charly-core's own init() populates — so both were rerouted through their own
// EXISTING seams (the host fleet-config loader, now retired by the loaderkit helper, and
// pod-config-box-engine) instead of calling deploykit directly.

// StartCmd launches a container with supervisord in the background — the `charly start` grammar.
type StartCmd struct {
	Box          string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version]) — the deploy of this box"`
	Tag          string   `name:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build        bool     `name:"build" help:"Force local build instead of pulling from registry"`
	Env          []string `short:"e" name:"env" sep:"none" help:"Set container env var (direct mode only)"`
	EnvFile      string   `name:"env-file" help:"Load env vars from file (direct mode only)"`
	Instance     string   `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Port         []string `short:"p" help:"Remap host port (direct mode only)"`
	VolumeFlag   []string `name:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind         []string `name:"bind" help:"Bind volume to host path (name or name=path)"`
	NoAutoDetect bool     `name:"no-auto-detect" help:"Disable automatic device detection"`
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
	return hostPodLifecycle("start", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodStartPayload{
		Tag:          c.Tag,
		Build:        c.Build,
		Env:          c.Env,
		EnvFile:      c.EnvFile,
		Port:         c.Port,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
	})
}

// lifecycleNode resolves the per-host deploy overlay entry to thread as DATA into the pod-lifecycle
// HostBuild requests (spec.PodLifecycleRequest.Node, #55 W3 A10b) — #55 K4 seam-completion: the host's
// dispatchLifecycleTarget operates on this *spec.Deploy instead of re-reading the per-host config
// itself (the config READ is a plugin loading capability, not a host M; #55 coneC Unit C2 moved
// the resolver from deploykit.ResolveLifecycleDeployNodeViaSeam — the deleted
// host fleet-config loader-seam round-trip — to the cycle-free plugin-side
// loaderkit.ResolveLifecycleDeployNodeViaExecutor, byte-identical to the retired core
// resolveLifecycleDeployNode). The box/instance MUST match the request's Box/Instance — the host
// derives deployName = DeployKey(req.Box, req.Instance), which must key the SAME node.
func lifecycleNode(box, instance string) *spec.Deploy {
	n, _ := loaderkit.ResolveLifecycleDeployNodeViaExecutor(cmdCtx, cmdExec, box, instance)
	return n
}

// StopCmd stops a running container started by StartCmd — the `charly stop` grammar.
type StopCmd struct {
	Box      string `arg:"" help:"Box name or remote ref — the deploy of this box"`
	Instance string `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Unmount  bool   `name:"unmount" help:"After stopping, also tear down encrypted FUSE mounts and gocryptfs scope units (charly-enc-<box>-<volume>.scope) for this box"`
}

func (c *StopCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// Resolve the image name (handle remote refs).
	boxName := c.Box
	ref := kit.StripURLScheme(c.Box)
	if spec.IsRemoteImageRef(ref) {
		boxName = spec.ParseRemoteRef(ref).Name
	}
	return hostPodLifecycle("stop", boxName, c.Instance, lifecycleNode(boxName, c.Instance), spec.PodStopPayload{
		Unmount: c.Unmount,
	})
}

// RestartCmd restarts a service container — the `charly restart` grammar. In quadlet mode it
// issues a single `systemctl --user restart`, which is atomic from systemd's perspective —
// ExecStopPost (e.g. tailscale serve --off) runs before ExecStartPost (tailscale serve), and the
// unit ends in either active or failed, never the silent stopped state a manual stop+start
// sequence can produce when start fails. NOT registry-bound (no ResolveTarget/plugin-loader need)
// — calls deploykit.RestartPodService directly, zero HostBuild round-trip.
type RestartCmd struct {
	Box      string `arg:"" help:"Box name or remote ref — the deploy of this box"`
	Instance string `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
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
// (dispatchLifecycleTarget/LifecycleTarget — core Mechanisms) — forwards via HostBuild("pod-lifecycle")
// op="logs" (#55 W3 A10b unified the former dedicated "pod-logs" kind into this one).
type LogsCmd struct {
	Box      string `arg:"" help:"Box name or remote ref — the deploy of this box"`
	Follow   bool   `short:"f" name:"follow" help:"Follow log output"`
	Instance string `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Sidecar  string `name:"sidecar" help:"Show the named SIDECAR container's logs instead of the app container's"`
}

func (c *LogsCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return hostPodLifecycle("logs", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodLogsPayload{
		Follow:  c.Follow,
		Sidecar: c.Sidecar,
	})
}

// RemoveCmd removes a service container — the `charly remove` grammar. Cutover B unit 2 remove-verb
// completion (option (b), full parity with the other 6 verbs): the plugin now owns the WHOLE
// orchestration itself — tunnel-stop (resolveContainerTunnel + podTunnelStop, remove_tunnel.go —
// verb:tunnel over InvokeProvider) and the quadlet/container-teardown/hook/cleanup body
// (runPodRemove, remove_orchestration.go), reaching the host only for the two genuinely
// host-coupled axes (the credential-backed hook env via the EXISTING pod-config-hook-secret-env
// seam, and the deploy-entry cleanup via the NEW pod-config-clean-deploy-entry seam — see
// remove_orchestration.go's header for why it needs its own host-owns-load+lock+mutate+save seam
// rather than the plugin-side whole-config deploy-state write). The
// arbiter-release bracket stays host-side under the "pod-lifecycle" HostBuild op="remove" (#55 W3
// A10b unified the former dedicated "pod-remove" kind into this one) — same shape as pod
// start/stop's own bracket — deferred here as the LAST step so it always runs, mirroring the
// former core `defer releaseResourceClaim(...)` semantics exactly.
type RemoveCmd struct {
	Box        string   `arg:"" help:"Box name or remote ref — the deploy of this box"`
	Instance   string   `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Purge      bool     `name:"purge" help:"Also remove named volumes"`
	KeepDeploy bool     `name:"keep-deploy" help:"Keep charly.yml entry for this box"`
	Env        []string `short:"e" name:"env" sep:"none" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	boxName := kit.ResolveBoxName(c.Box)
	defer func() {
		_ = hostPodLifecycle("remove", boxName, c.Instance, nil, spec.PodRemovePayload{})
	}()

	if tc := resolveContainerTunnel(c.Box, c.Instance); tc != nil {
		if err := podTunnelStop(tc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
	return runPodRemove(c.Box, c.Instance, c.Purge, c.KeepDeploy, c.Env)
}

// ShellCmd starts a bash shell in a container image — the `charly shell` grammar. Registry-bound
// (dispatchLifecycleTarget/LifecycleTarget — core Mechanisms) — forwards via HostBuild("pod-lifecycle")
// op="shell" (#55 W3 A10b unified the former dedicated "pod-shell" kind into this one).
type ShellCmd struct {
	Box          string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version]) — the deploy of this box"`
	Tag          string   `name:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Command      string   `short:"c" help:"Command to execute instead of interactive shell"`
	Build        bool     `name:"build" help:"Force local build instead of pulling from registry"`
	TTY          bool     `name:"tty" help:"Force TTY allocation (for automation tools that lack a real terminal)"`
	Env          []string `short:"e" name:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	EnvFile      string   `name:"env-file" help:"Load env vars from file"`
	Instance     string   `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	VolumeFlag   []string `name:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind         []string `name:"bind" help:"Bind volume to host path (name or name=path)"`
	NoAutoDetect bool     `name:"no-auto-detect" help:"Disable automatic device detection"`
}

func (c *ShellCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`. Users must pull
	// first, then run shell on the short name.
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly shell <image-name>'", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	return hostPodLifecycle("shell", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodShellPayload{
		Tag:          c.Tag,
		Command:      c.Command,
		Build:        c.Build,
		TTY:          c.TTY,
		Env:          c.Env,
		EnvFile:      c.EnvFile,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
	})
}

// ServiceCmd manages services inside a running container — the `charly service` grammar. Cutover
// B unit 2 completion: each leaf now resolves + validates + renders the FULL argv itself
// (buildServiceArgv, service_resolve.go — all portable) and forwards it via ONE HostBuild
// ("pod-lifecycle") op="service" seam (#55 W3 A10b unified the former dedicated "pod-service"
// kind into this one) whose host body does ONLY dispatchLifecycleTarget + LifecycleTarget.Shell
// (the irreducible registry-bound step).
type ServiceCmd struct {
	Restart ServiceRestartCmd `cmd:"" help:"Restart an in-container service"`
	Start   ServiceStartCmd   `cmd:"" help:"Start an in-container service"`
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of in-container services"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop an in-container service"`
}

// ServiceStatusCmd shows status of all services
type ServiceStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "status", "")
	if err != nil {
		return err
	}
	return hostPodLifecycle("service", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodServicePayload{Argv: argv})
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "start", c.Service)
	if err != nil {
		return err
	}
	return hostPodLifecycle("service", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodServicePayload{Argv: argv})
}

// ServiceStopCmd stops a service
type ServiceStopCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "stop", c.Service)
	if err != nil {
		return err
	}
	return hostPodLifecycle("service", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodServicePayload{Argv: argv})
}

// ServiceRestartCmd restarts a service
type ServiceRestartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	argv, err := buildServiceArgv(c.Box, c.Instance, "restart", c.Service)
	if err != nil {
		return err
	}
	return hostPodLifecycle("service", c.Box, c.Instance, lifecycleNode(c.Box, c.Instance), spec.PodServicePayload{Argv: argv})
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
	Instance string `short:"i" name:"instance" help:"Instance name"`
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
	Instance string `short:"i" name:"instance" help:"Instance name"`
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
	Instance string `short:"i" name:"instance" help:"Instance name"`
	Sidecar  string `name:"sidecar" help:"Target the named SIDECAR container instead of the app container"`
}

// ConfigCmd groups box configuration subcommands — the `charly config` grammar. Default
// subcommand (no keyword): full setup (quadlet + secrets + enc). Every leaf's actual body is
// deeply core-type-coupled (FleetConfig/ResolvedSidecar/enc*/deploykit.CleanDeployEntry, and
// Setup is ALSO constructed directly, by its EXACT unchanged name, by from_box_pod.go (the
// `charly fleet from-box` pod path, K-wave 2 cone R2 — formerly charly/fleet_from_box_cmd.go) —
// P13-kernel, out of this wave's scope — so the core struct cannot rename/move), so each leaf
// forwards via its own HostBuild("pod-config-<leaf>") seam.
type ConfigCmd struct {
	Mount   ConfigMountCmd   `cmd:"mount" help:"Mount encrypted volumes"`
	Passwd  ConfigPasswdCmd  `cmd:"passwd" help:"Change gocryptfs password"`
	Remove  ConfigRemoveCmd  `cmd:"remove" help:"Disable quadlet service (quadlet file remains)"`
	Setup   ConfigSetupCmd   `cmd:"" default:"withargs" help:"Setup quadlet, secrets, and encrypted volumes"`
	Status  ConfigStatusCmd  `cmd:"status" help:"Show encrypted volume status"`
	Unmount ConfigUnmountCmd `cmd:"unmount" help:"Unmount encrypted volumes"`
}

// ConfigSetupCmd configures a box: generates quadlet, provisions secrets, initializes and mounts
// encrypted volumes — the `charly config [setup]` grammar (mirrors core's BoxConfigSetupCmd 1:1).
type ConfigSetupCmd struct {
	Box           string   `arg:"" optional:"" help:"Box name or remote ref (github.com/org/repo/box[@version]) — the deploy of this box"`
	Tag           string   `name:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build         bool     `name:"build" help:"Force local build instead of pulling from registry"`
	Env           []string `short:"e" name:"env" sep:"none" help:"Set container env var (KEY=VALUE), merged with existing vars"`
	Clean         bool     `short:"c" name:"clean" help:"Replace all env vars instead of merging (clean slate)"`
	EnvFile       string   `name:"env-file" help:"Load env vars from file"`
	Instance      string   `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Port          []string `short:"p" help:"Remap host port (newHost:containerPort, e.g., 5901:5900)"`
	KeepMounted   bool     `name:"keep-mounted" help:"Keep encrypted volumes mounted after setup"`
	Password      string   `name:"password" default:"auto" enum:"auto,manual" help:"auto: generate secrets (default), manual: prompt for each"`
	RefreshSecret []string `name:"refresh-secret" help:"Force re-provisioning of the named podman secret(s) from their source on this run ('all' = every secret of this image, sidecars included): the charly-<image>-<name> secret is removed and recreated. A candy-owned auto-generated secret gets a NEW value — re-initialize services that stored the old one"`
	VolumeFlag    []string `name:"volume" short:"v" help:"Configure volume backing (name:type[:path]). Type: volume|bind|encrypted"`
	Bind          []string `name:"bind" help:"Shorthand: configure volume as bind mount (name or name=path)"`
	Encrypt       []string `name:"encrypt" help:"Shorthand: configure volume as encrypted (gocryptfs)"`
	MemoryMax     string   `name:"memory-max" help:"Cgroup memory.max hard OOM limit (e.g. 6g, 500m). Persists to charly.yml."`
	MemoryHigh    string   `name:"memory-high" help:"Cgroup memory.high soft limit — reclaim pressure before OOM. Persists to charly.yml."`
	MemorySwapMax string   `name:"memory-swap-max" help:"Cgroup memory.swap.max ceiling. Persists to charly.yml."`
	Cpus          string   `name:"cpus" help:"CPU quota in cores (e.g. 2.5 for 2.5 cores). Persists to charly.yml."`
	Seed          bool     `name:"seed" default:"true" negatable:"" help:"Seed bind-backed volumes with data from image (default: true)"`
	ForceSeed     bool     `name:"force-seed" help:"Re-seed even if target directory is not empty"`
	DataFrom      string   `name:"data-from" help:"Seed data from this data image instead of the target image"`
	UpdateAll     bool     `name:"update-all" help:"Regenerate quadlets for all other deployed boxes to pick up env_provides changes"`
	SshKey        string   `name:"ssh-key" help:"SSH public key: 'auto' (default ~/.ssh key), path to .pub file, 'generate', or 'none'"`
	Sidecar       []string `name:"sidecar" help:"Attach sidecar (from built-in templates, e.g. 'tailscale')"`
	ListSidecars  bool     `name:"list-sidecars" help:"List available sidecar templates and exit"`
	NoAutoDetect  bool     `name:"no-auto-detect" help:"Disable automatic device detection"`
}

func (c *ConfigSetupCmd) Run() error {
	reqJSON, err := json.Marshal(spec.PodConfigSetupRequest{
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
		SSHKey:        c.SshKey,
		Sidecar:       c.Sidecar,
		ListSidecars:  c.ListSidecars,
		NoAutoDetect:  c.NoAutoDetect,
		// HostEnvJSON is threaded as DATA on the OpRun dispatch (compiled-in ⇒ this plugin's own
		// os.Executable() would be correct, but the host computes it once for every command) —
		// deploy:pod's encrypted-mount ExecStartPre CharlyBin line reads it.
		HostEnvJSON: cmdHostEnvJSON,
	})
	if err != nil {
		return fmt.Errorf("config setup: %w", err)
	}
	resJSON, err := dispatchPodConfigOp(sdk.OpConfigSetup, reqJSON)
	if err != nil {
		return err
	}
	// The deploy:pod plugin is out-of-process, so ITS stdout (where the former in-plugin
	// --list-sidecars print lived) is go-plugin-discarded — the list now returns in the reply
	// and is printed HERE, in the charly CLI's own stdio (the P13-KERNEL pre-existing
	// invisible-output bug fixed with the list-sidecars leg relocation, K-wave 2 cone R3).
	var rep spec.PodConfigSetupReply
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &rep)
	}
	if len(rep.SidecarList.Names) > 0 {
		names := append([]string{}, rep.SidecarList.Names...)
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("%-20s %s\n", name, rep.SidecarList.Descriptions[name])
		}
	}
	return nil
}

// ConfigStatusCmd shows status of all services
type ConfigStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ConfigStatusCmd) Run() error {
	return pluginEncStatus(c.Box, c.Instance)
}

// ConfigMountCmd mounts encrypted volumes.
type ConfigMountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `name:"volume" help:"Only mount this volume (by name)"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ConfigMountCmd) Run() error {
	return pluginEncMount(c.Box, c.Instance, c.Volume)
}

// ConfigUnmountCmd unmounts encrypted volumes.
type ConfigUnmountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `name:"volume" help:"Only unmount this volume (by name)"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ConfigUnmountCmd) Run() error {
	return pluginEncUnmount(c.Box, c.Instance, c.Volume)
}

// ConfigPasswdCmd changes the gocryptfs password.
type ConfigPasswdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ConfigPasswdCmd) Run() error {
	return pluginEncPasswd(c.Box, c.Instance)
}

// ConfigRemoveCmd removes a quadlet service (replaces charly disable).
type ConfigRemoveCmd struct {
	Box      string `arg:"" help:"Box name or remote ref — the deploy of this box"`
	Instance string `short:"i" name:"instance" help:"Instance name"`
}

func (c *ConfigRemoveCmd) Run() error {
	reqJSON, err := json.Marshal(spec.PodConfigRemoveRequest{Box: c.Box, Instance: c.Instance})
	if err != nil {
		return fmt.Errorf("config remove: %w", err)
	}
	if _, err := dispatchPodConfigOp(sdk.OpConfigRemove, reqJSON); err != nil {
		return err
	}
	return nil
}

// UpdateCmd updates an image (pulls/builds the latest), preserves the existing deploy config
// (user-overlay state untouched), and restarts the service to pick up the new image — the
// `charly update` grammar. Resolves the deploy tree PLUGIN-SIDE (loaderkit.ResolveMergedTreeViaExecutor,
// #55 Cone A Unit 3b) and threads it in; the host's remaining loadDeployPlugins/ResolveTarget are
// core Mechanisms — forwards via HostBuild("pod-lifecycle") op="update" (#55 W3 A10b unified the
// former dedicated "pod-update" kind into this one).
type UpdateCmd struct {
	Box       string `arg:"" help:"Deploy name (resolved via charly.yml) OR box name. For deploys, the target's update strategy is auto-selected (pod=systemctl restart with new image; vm=in-guest candy re-apply; local=idempotent re-apply)."`
	Tag       string `name:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build     bool   `name:"build" help:"Force local build instead of pulling from registry"`
	Instance  string `short:"i" name:"instance" help:"Instance name for running multiple containers of the same box"`
	Seed      bool   `name:"seed" default:"true" negatable:"" help:"Sync data from new image into bind-backed volumes (default: true)"`
	ForceSeed bool   `name:"force-seed" help:"Overwrite existing data in volumes (default: only add new files)"`
	DataFrom  string `name:"data-from" help:"Sync data from this data image instead"`
}

func (c *UpdateCmd) Run() error {
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	// Resolve the deploy node PLUGIN-SIDE (merged tree → full deploy key) and thread the node
	// into the seam as DATA — the K-wave 2 cone CONTESTED completion of the #55 Cone A Unit 3b
	// tree-threading: the host's dispatchByDeployTarget needs the node, not the whole tree.
	node, err := resolveUpdateDeployNode(c.Box, c.Instance)
	if err != nil {
		return err
	}
	noteUpdateDisposability(node, c.Box, c.Instance)
	return hostPodLifecycle("update", c.Box, c.Instance, node, spec.PodUpdatePayload{
		Tag:       c.Tag,
		Build:     c.Build,
		Seed:      c.Seed,
		ForceSeed: c.ForceSeed,
		DataFrom:  c.DataFrom,
	})
}

// resolveUpdateDeployNode resolves the deploy entry for an `charly update` invocation by the FULL
// deploy key, resolving the merged project+operator deploy tree PLUGIN-SIDE
// (loaderkit.ResolveMergedTreeViaExecutor) and looking the key up in it. deployKey applies the
// -i instance, returning the bare (or dotted-nested) name unchanged when instance is empty — so
// `charly update <base> -i <inst>` finds the instance-keyed `<base>/<inst>` entry, plain names
// still resolve, and dotted nested paths (`a.b.c`) still walk. On miss the error reports the full
// key. The "deploy-plugins-connect" preamble connects the deployment's out-of-tree plugin candies
// (the host's ResolveTarget needs them) and returns the project dir the loader loads from — the
// SAME preamble command:fleet's resolveTreeViaLoader runs. Relocated from
// charly/update_deploy_dispatch.go (K-wave 2 cone CONTESTED).
func resolveUpdateDeployNode(image, instance string) (*spec.Deploy, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("pod update: no host reverse channel (command not compiled-in?)")
	}
	var pre spec.DeployPluginsConnectReply
	if err := hostPodSeamReply("deploy-plugins-connect", spec.DeployPluginsConnectRequest{Path: image}, &pre); err != nil {
		return nil, err
	}
	tree, err := loaderkit.ResolveMergedTreeViaExecutor(cmdCtx, cmdExec, pre.Dir)
	if err != nil {
		return nil, err
	}
	return lookupDeployNode(tree, image, instance)
}

// lookupDeployNode walks the merged deploy tree for the FULL deploy key — the pure
// fleet.ResolveNodePath step, split out for the unit test. deployKey applies the -i instance
// (returning the bare or dotted-nested name unchanged when instance is empty), so an
// instance-only `<base>/<inst>` entry resolves and a bare-base lookup correctly does NOT match it.
func lookupDeployNode(tree map[string]spec.FleetNode, image, instance string) (*spec.Deploy, error) {
	key := spec.DeployKey(image, instance)
	node, _, err := fleet.ResolveNodePath(tree, key)
	if err != nil || node == nil {
		return nil, fmt.Errorf("no deploy named %q in charly.yml. To refresh an image artifact only, use 'charly box pull %s'", key, image)
	}
	return node, nil
}

// noteUpdateDisposability prints a one-line transparency note when an EXPLICIT `charly update`
// targets a deploy that is NOT marked `disposable: true` (and not ephemeral — see IsDisposable()
// for the implication chain). It NEVER refuses: `charly update` is a human-driven verb that obeys
// any explicit invocation on any target. The `disposable:` flag remains load-bearing as the
// authorization for the AI's AUTONOMOUS destroy + rebuild (CLAUDE.md R10) and for the
// check-runner's unattended fresh-rebuild (validateCheckBeds) — it just no longer gates this
// command. The note lets an operator catch a mistyped name before the rebuild proceeds.
// Relocated from charly/update_deploy_dispatch.go (K-wave 2 cone CONTESTED).
func noteUpdateDisposability(node *spec.Deploy, image, instance string) {
	if node == nil || node.IsDisposable() {
		return
	}
	key := spec.DeployKey(image, instance)
	lifecycle := node.Lifecycle
	if lifecycle == "" {
		lifecycle = "(unset)"
	}
	fmt.Fprintf(os.Stderr,
		"Note: %q is not marked `disposable: true` (lifecycle: %s); rebuilding it anyway per your explicit `charly update`.\n",
		key, lifecycle)
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
