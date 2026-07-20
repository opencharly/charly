package pod

import (
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
