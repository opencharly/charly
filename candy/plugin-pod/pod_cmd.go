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
// <word> …` Kong tree moved OUT of charly core into this plugin candy; a REGISTRY-BOUND leaf
// (start/stop) forwards its authored flags, as the matching sdk/spec wire request, to its
// HostBuild seam via hostPodSeam — the host reconstructs the core orchestration struct and runs
// its Run() logic VERBATIM (mirroring candy/plugin-bundle/bundle_cmd.go). A leaf with NO registry
// coupling (restart) calls sdk/deploykit directly — no seam needed.

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
	NoAutoDetect bool      `long:"no-autodetect" help:"Disable automatic device detection"`
}

func (c *StartCmd) Run() error {
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
	return hostPodSeam("pod-stop", spec.PodStopRequest{
		Box:      c.Box,
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
	return hostPodSeam("pod-logs", spec.PodLogsRequest{
		Box:      c.Box,
		Follow:   c.Follow,
		Instance: c.Instance,
		Sidecar:  c.Sidecar,
	})
}

// RemoveCmd removes a service container — the `charly remove` grammar. Deeply core-type-coupled
// (not registry-bound, but not portable) — forwards via HostBuild("pod-remove").
type RemoveCmd struct {
	Box        string   `arg:"" help:"Box name or remote ref"`
	Instance   string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Purge      bool     `long:"purge" help:"Also remove named volumes"`
	KeepDeploy bool     `name:"keep-deploy" help:"Keep charly.yml entry for this box"`
	Env        []string `short:"e" long:"env" sep:"none" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	return hostPodSeam("pod-remove", spec.PodRemoveRequest{
		Box:        c.Box,
		Instance:   c.Instance,
		Purge:      c.Purge,
		KeepDeploy: c.KeepDeploy,
		Env:        c.Env,
	})
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

// ServiceCmd manages services inside a running container — the `charly service` grammar. All four
// leaves share resolveServiceInit + execInitCommand host-side (registry-bound, not portable), so
// they forward via ONE HostBuild("pod-service") seam with an Operation discriminator.
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
	return hostPodSeam("pod-service", spec.PodServiceRequest{Operation: "status", Box: c.Box, Instance: c.Instance})
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	return hostPodSeam("pod-service", spec.PodServiceRequest{Operation: "start", Box: c.Box, Service: c.Service, Instance: c.Instance})
}

// ServiceStopCmd stops a service
type ServiceStopCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	return hostPodSeam("pod-service", spec.PodServiceRequest{Operation: "stop", Box: c.Box, Service: c.Service, Instance: c.Instance})
}

// ServiceRestartCmd restarts a service
type ServiceRestartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	return hostPodSeam("pod-service", spec.PodServiceRequest{Operation: "restart", Box: c.Box, Service: c.Service, Instance: c.Instance})
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
