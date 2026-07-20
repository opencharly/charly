package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// BoxConfigCmd groups box configuration subcommands.
// Default subcommand (no keyword): full setup (quadlet + secrets + enc).
type BoxConfigCmd struct {
	Mount   BoxConfigMountCmd   `cmd:"mount" help:"Mount encrypted volumes"`
	Passwd  BoxConfigPasswdCmd  `cmd:"passwd" help:"Change gocryptfs password"`
	Remove  BoxConfigRemoveCmd  `cmd:"remove" help:"Remove quadlet and disable service"`
	Setup   BoxConfigSetupCmd   `cmd:"" default:"withargs" help:"Setup quadlet, secrets, and encrypted volumes"`
	Status  BoxConfigStatusCmd  `cmd:"status" help:"Show encrypted volume status"`
	Unmount BoxConfigUnmountCmd `cmd:"unmount" help:"Unmount encrypted volumes"`
}

// BoxConfigSetupCmd configures a box: generates quadlet, provisions secrets,
// initializes and mounts encrypted volumes.
type BoxConfigSetupCmd struct {
	Box             string   `arg:"" optional:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag             string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Build           bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env             []string `short:"e" long:"env" sep:"none" help:"Set container env var (KEY=VALUE), merged with existing vars"`
	Clean           bool     `short:"c" long:"clean" help:"Replace all env vars instead of merging (clean slate)"`
	EnvFile         string   `long:"env-file" help:"Load env vars from file"`
	Instance        string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	Port            []string `short:"p" help:"Remap host port (newHost:containerPort, e.g., 5901:5900)"`
	KeepMounted     bool     `long:"keep-mounted" help:"Keep encrypted volumes mounted after setup"`
	Password        string   `long:"password" default:"auto" enum:"auto,manual" help:"auto: generate secrets (default), manual: prompt for each"`
	RefreshSecret   []string `name:"refresh-secret" help:"Force re-provisioning of the named podman secret(s) from their source on this run ('all' = every secret of this image, sidecars included): the charly-<image>-<name> secret is removed and recreated. A candy-owned auto-generated secret gets a NEW value — re-initialize services that stored the old one"`
	VolumeFlag      []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path]). Type: volume|bind|encrypted"`
	Bind            []string `long:"bind" help:"Shorthand: configure volume as bind mount (name or name=path)"`
	Encrypt         []string `long:"encrypt" help:"Shorthand: configure volume as encrypted (gocryptfs)"`
	MemoryMax       string   `long:"memory-max" help:"Cgroup memory.max hard OOM limit (e.g. 6g, 500m). Persists to charly.yml."`
	MemoryHigh      string   `long:"memory-high" help:"Cgroup memory.high soft limit — reclaim pressure before OOM. Persists to charly.yml."`
	MemorySwapMax   string   `long:"memory-swap-max" help:"Cgroup memory.swap.max ceiling. Persists to charly.yml."`
	Cpus            string   `long:"cpus" help:"CPU quota in cores (e.g. 2.5 for 2.5 cores). Persists to charly.yml."`
	Seed            bool     `long:"seed" default:"true" negatable:"" help:"Seed bind-backed volumes with data from image (default: true)"`
	ForceSeed       bool     `long:"force-seed" help:"Re-seed even if target directory is not empty"`
	DataFrom        string   `long:"data-from" help:"Seed data from this data image instead of the target image"`
	UpdateAll       bool     `long:"update-all" help:"Regenerate quadlets for all other deployed boxes to pick up env_provides changes"`
	SshKey          string   `long:"ssh-key" help:"SSH public key: 'auto' (default ~/.ssh key), path to .pub file, 'generate', or 'none'"`
	Sidecar         []string `long:"sidecar" help:"Attach sidecar (from built-in templates, e.g. 'tailscale')"`
	ListSidecars    bool     `long:"list-sidecars" help:"List available sidecar templates and exit"`
	AutoDetectFlags `embed:""`

	// ExplicitRef, when non-empty, bypasses the short-name → registry-ref
	// resolution in runConfig and uses this exact image ref (a full local or
	// registry ref). Set by `charly bundle from-box` for a source-less pod
	// deploy — quadlet config comes from the image's baked OCI labels with no
	// charly.yml project. Box then carries the deploy-key/name only.
	// Not a CLI flag (kong:"-").
	ExplicitRef string `kong:"-"`
}

func (c *BoxConfigSetupCmd) Run() error {
	if c.ListSidecars {
		templates, err := embeddedSidecarBodies()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(templates))
		for name := range templates {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// Peek only the description from the opaque body for the listing — the
			// kernel does not type sidecar bodies (the sidecar de-type, Cutover D).
			var meta struct {
				Description string `json:"description"`
			}
			_ = json.Unmarshal(templates[name], &meta)
			fmt.Printf("%-20s %s\n", name, meta.Description)
		}
		return nil
	}

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	switch rt.RunMode {
	case "quadlet", "direct":
		// Both modes are supported. Direct mode skips quadlet/systemctl
		// and uses `podman run -d` directly — used in nested environments
		// (harness sandbox pods, supervisord-only containers, sysvinit hosts) where
		// systemd-user is unavailable. The branch point is inside
		// runConfig at the quadlet-write step.
	default:
		return fmt.Errorf("charly config requires run_mode=quadlet or direct (current: %s)", rt.RunMode)
	}

	if c.Box == "" {
		return fmt.Errorf("image name is required")
	}

	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly config <image-name>'", c.Box)
	}

	// Canonicalize Pattern A "<base>/<instance>" so downstream code uses
	// the (image, instance) split — without this, MergeDeployOntoMetadata
	// looks up the wrong charly.yml key and drops port/env overlays.
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)
	if err := deploykit.RejectImageRefAsDeployName(c.Box); err != nil {
		return err
	}

	return c.runConfig(rt)
}

// resolveDeployRef resolves the deploy-key positional arg into the deploy box
// short-name and the image ref the rest of the config pipeline pulls/inspects.
//
// Pattern B (arbitrary deploy-key + version-pin) lookup —
// /charly-core:deploy "Two supported deploy patterns". If `c.Box`
// (the positional arg) names a charly.yml entry with an
// explicit `box:` field, use that as the ref the rest of the
// pipeline pulls/inspects. Critically c.Box is NOT mutated:
// it remains the deploy-key for container-name / quadlet-name
// / secret-name / charly.yml-key composition. Pre-2026-05-12
// the arg was always treated as a kind:box short-name; this
// split lets the deploy-key and the image-ref diverge.
// Resolve the deploy key to its declared box short-name via THE shared
// resolver (deploy.go resolveDeployBoxName) that config / start / shell
// / check live all use, so they never diverge. Falls back to the key for
// the key==box convention. c.Box stays the deploy-KEY for container /
// quadlet / secret / charly.yml-key composition; only the image ref and
// the persisted `box:` field use the resolved name. Routing the
// short name through resolveShellImageRef yields a full local-CalVer ref
// podman storage knows (storage is keyed by full registry refs like
// ghcr.io/opencharly/arch:TAG, not bare short names).
func (c *BoxConfigSetupCmd) resolveDeployRef() (deployBoxName, imageRef string) {
	if c.ExplicitRef != "" {
		// Source-less from-box deploy (`charly bundle from-box`): use the exact
		// ref as-is; c.Box is the deploy-key/name only. No charly.yml
		// short-name resolution, no registry-ref composition — the image is
		// already present locally (e.g. cp-box'd into a VM guest) and its
		// quadlet config comes entirely from its baked OCI labels.
		//
		// PERSIST THE FULL REF (not the deploy key) as the charly.yml `box:`
		// value, so the deploy is self-describing: a later project-FREE command
		// — `charly check live <name>`, `charly status`, `charly config <name>` on a host
		// with no charly.yml (e.g. a VM guest, where the nested-pod check is
		// delegated) — resolves the image straight from local storage (full refs
		// pass through resolveImageRefForEnsure unchanged) instead of failing
		// with "short name requires a project directory with charly.yml". The
		// deploy KEY stays c.Box for container/quadlet/secret naming.
		deployBoxName = c.ExplicitRef
		imageRef = c.ExplicitRef
	} else {
		deployBoxName = resolveDeployBoxName(c.Box, c.Instance)
		if deployBoxName != c.Box {
			fmt.Fprintf(os.Stderr, "config: deploy %q declares box: %q\n", c.Box, deployBoxName)
		}
		// Prefer the persisted overlay ref: PrepareVenue records the EXACT
		// <deploy-key>-overlay:<hash> an add_candy: build produced, so config
		// deploys the OVERLAY (carrying the add_candy: layers) instead of the
		// base image: short-name re-resolved by a CalVer sort the overlay alias
		// can lose to the base on a same-minute build (the add_candy-on-pod
		// deploy-resolution quirk). Empty for a plain pod, or when the recorded
		// overlay is gone (post-`bundle del`) → fall back to base-name resolution.
		if ov := resolveDeployResolvedImage(c.Box, c.Instance); ov != "" && kit.LocalImageExists("podman", ov) {
			imageRef = ov
		} else {
			imageRef = resolveShellImageRef("", deployBoxName, c.Tag)
		}
	}
	return deployBoxName, imageRef
}

// prepareQuadletEnv resolves the EnvironmentFile= path for the quadlet:
// CLI --env-file > charly.yml env_file > workspace .env. Split out of runConfig.
func (c *BoxConfigSetupCmd) prepareQuadletEnv(dc *deploykit.BundleConfig, bindMounts []deploykit.ResolvedBindMount) string {
	var quadletEnvFile string
	if c.EnvFile != "" {
		quadletEnvFile, _ = filepath.Abs(c.EnvFile)
	}
	// Check charly.yml env_file
	if quadletEnvFile == "" && dc != nil {
		if overlay, ok := dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)]; ok && overlay.EnvFile != "" {
			quadletEnvFile = kit.ExpandHostHome(overlay.EnvFile)
		}
	}
	// Also check workspace .env for quadlet EnvironmentFile
	if quadletEnvFile == "" {
		if wsHost := workspaceBindHost(bindMounts); wsHost != "" {
			wsEnvPath := filepath.Join(wsHost, ".env")
			if _, statErr := os.Stat(wsEnvPath); statErr == nil {
				quadletEnvFile = wsEnvPath
			}
		}
	}
	return quadletEnvFile
}

// resolveSidecars resolves sidecars (embedded templates + charly.yml +
// --sidecar flags), routes CLI -e flags to the matching sidecar (mutating
// c.Env to the app-only set), and provisions sidecar secrets (appending any
// fallback env to envVars). Returns the deploy sidecar defs, the resolved
// sidecars, and the (possibly extended) env var list. Split out of runConfig.
func (c *BoxConfigSetupCmd) resolveSidecars(dc *deploykit.BundleConfig, rt *kit.ResolvedRuntime, autoGen bool, envVars []string) (map[string]json.RawMessage, []deploykit.ResolvedSidecar, []string, error) {
	// Gather the per-deploy sidecar overrides (OPAQUE bodies) from the deploy node
	// + any --sidecar flags (an empty override inherits the template).
	var deploySidecars map[string]json.RawMessage
	if dc != nil {
		if overlay, ok := dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)]; ok {
			deploySidecars = overlay.Sidecar
		}
	}
	for _, scName := range c.Sidecar {
		if deploySidecars == nil {
			deploySidecars = make(map[string]json.RawMessage)
		}
		if _, ok := deploySidecars[scName]; !ok {
			deploySidecars[scName] = json.RawMessage("{}") // empty override, inherits from template
		}
	}
	if len(deploySidecars) == 0 {
		return deploySidecars, nil, envVars, nil
	}

	// candy/plugin-sidecar owns ALL sidecar business logic (Cutover D): it routes
	// CLI -e flags to sidecars (app-only env returned), merges embedded < project <
	// deploy templates, and resolves volume/secret names + env_from.
	embedded, err := embeddedSidecarBodies()
	if err != nil {
		return nil, nil, envVars, fmt.Errorf("resolving sidecars: %w", err)
	}
	reply, err := resolveSidecarsViaPlugin(spec.SidecarResolveInput{
		EmbeddedTemplates: embedded,
		ProjectTemplates:  sidecarTemplatesOf(dc),
		DeployOverrides:   deploySidecars,
		CliEnv:            c.Env,
		Box:               c.Box,
		Instance:          c.Instance,
	})
	if err != nil {
		return nil, nil, envVars, fmt.Errorf("resolving sidecars: %w", err)
	}
	// The plugin routed sidecar env vars out of the app env + folded them into the
	// deploy overrides to persist (opaque bodies).
	c.Env = reply.AppEnv
	deploySidecars = reply.PersistOverrides
	resolvedSidecars := make([]deploykit.ResolvedSidecar, 0, len(reply.Sidecars))
	for _, rs := range reply.Sidecars {
		resolvedSidecars = append(resolvedSidecars, resolvedSidecarFromSpec(rs))
	}

	// Provision sidecar secrets as podman secrets
	for i, sc := range resolvedSidecars {
		if len(sc.Secret) > 0 {
			scSecrets, _ := ApplySecretRefresh(sc.Secret, c.RefreshSecret)
			scProvisioned, scFallback, scErr := ProvisionPodmanSecrets(rt.RunEngine, c.Box, c.Instance, scSecrets, autoGen)
			if scErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not provision sidecar %s secrets: %v\n", sc.Name, scErr)
			}
			resolvedSidecars[i].Secret = scProvisioned
			for _, kv := range scFallback {
				envVars = appendEnvUnique(envVars, kv)
			}
		}
	}

	return deploySidecars, resolvedSidecars, envVars, nil
}

//nolint:gocyclo // sequential charly-config deploy pipeline (ref resolution → metadata merge → ports → env/secrets → sidecars → quadlet/systemd write → data seed → hooks); phases consume the previous phase's locals (meta/dc/envVars/ports). Major phases extracted (resolveDeployRef/prepareQuadletEnv/resolveSidecars); the residual orchestration is irreducibly above threshold without unwieldy multi-value param passing
func (c *BoxConfigSetupCmd) runConfig(rt *kit.ResolvedRuntime) error {
	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	// Resolve the deploy-key positional arg to (deploy-box-name, image-ref).
	// c.Box stays the deploy-KEY for container/quadlet/secret/charly.yml-key
	// composition; only the returned ref/name use the resolved box short-name.
	// See resolveDeployRef for the Pattern-B + from-box details.
	deployBoxName, imageRef := c.resolveDeployRef()
	podmanRT := &kit.ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
	if err := EnsureImage(imageRef, podmanRT); err != nil {
		return err
	}
	meta, err := ExtractMetadata("podman", imageRef)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", imageRef)
	}

	// Apply charly.yml overrides onto label metadata
	dc := deploykit.LoadDeployConfigForRead("charly config")

	// One-time migration: move any plaintext credentials that live in
	// charly.yml's env: list from the legacy -e flow into the credential
	// store, and clean them out of charly.yml on disk. Runs BEFORE
	// MergeDeployOntoMetadata so the cleaned deploy state is what gets
	// merged into meta. Idempotent no-op after the first successful run.
	// Plan §2.4.
	if _, err := MigratePlaintextEnvSecret(dc, meta, c.Box, c.Instance); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not migrate plaintext env secrets: %v\n", err)
	}

	// Pre-scrub CLI -e flags: any -e NAME=VAL where NAME is declared as a
	// secret_accepts/secret_requires entry is moved into the credential
	// store and stripped from c.Env before it can reach saveDeployState or
	// the quadlet writer. Plan §2.5.
	scrubbed, _ := scrubSecretCLIEnv(c.Env, meta)
	c.Env = scrubbed

	// Persist any --memory-max / --memory-high / --memory-swap-max / --cpus
	// flags into charly.yml so they survive across runs, and so that
	// MergeDeployOntoMetadata picks them up below on this run.
	if err := c.persistResourceCaps(&dc); err != nil {
		return fmt.Errorf("persisting resource caps: %w", err)
	}

	// Auto-port-mapping default: resolve a host:container publish mapping for
	// EVERY image-declared container port (meta.Port, from the OCI label) BEFORE
	// MergeDeployOntoMetadata, so the merge sees a concrete list. Each port gets
	// a fresh free 127.0.0.1 host port unless a deploy `port:` entry PINS it; a
	// prior allocation is reused for stability across `charly update`. The result
	// persists as ResolvedPort (re-read after the reload below) so charly
	// start/logs/status publish the same mapping. `OccupiedHostPorts` excludes
	// THIS deploy so two concurrent beds never collide on a host port.
	if dc != nil {
		key := deploykit.DeployKey(c.Box, c.Instance)
		overlay := dc.Bundle[key]
		containerPorts := ContainerPortsFromMappings(meta.Port)
		if len(containerPorts) > 0 || len(overlay.Port) > 0 {
			resolved, rErr := ResolveDeployPorts(containerPorts, overlay.Port, overlay.ResolvedPort, dc.OccupiedHostPorts(key))
			if rErr != nil {
				return fmt.Errorf("resolving deploy ports: %w", rErr)
			}
			if !SameStringSlice(overlay.ResolvedPort, resolved) {
				overlay.ResolvedPort = resolved
				dc.Bundle[key] = overlay
				if saveErr := saveBundleConfigNodeForm(dc); saveErr != nil {
					return fmt.Errorf("saving resolved_port: %w", saveErr)
				}
				fmt.Fprintf(os.Stderr, "Resolved ports for %s: %s\n",
					key, strings.Join(resolved, ", "))
			}
		}
	}

	deploykit.MergeDeployOntoMetadata(meta, dc, c.Box, c.Instance)

	uid, gid := meta.UID, meta.GID
	ports := meta.Port
	security := meta.Security
	network := meta.Network

	// Parse volume flags into deploy volume configs (CLI > env > charly.yml)
	deployVolumes := c.parseVolumeFlags()
	if len(deployVolumes) == 0 {
		deployVolumes = parseVolumeEnv(c.Box)
	}
	if len(deployVolumes) == 0 && dc != nil {
		if overlay, ok := dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)]; ok {
			deployVolumes = overlay.Volume
		}
	}

	// Resolve volume backing from labels + deploy config
	volumes, bindMounts := deploykit.ResolveVolumeBacking(c.Box, c.Instance, meta.Volume, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	// Re-resolve the canonical registry ref UNLESS the operator
	// supplied an explicit ref via the deploy entry's `box:`
	// field (Pattern B — arbitrary deploy-key + version-pin from
	// /charly-core:deploy "Two supported deploy patterns"). In Pattern
	// B the imageRef is already a fully-qualified registry ref
	// (e.g. `ghcr.io/opencharly/versa:2026.131.2134`) — pinning
	// to that exact tag is the whole point, so don't substitute
	// the deploy-key into a freshly-composed ref.
	// Skip the registry re-resolution when imageRef is the persisted overlay ref
	// (an add_candy: pod): re-composing meta.Registry/<base>:<tag> here would
	// throw away the overlay and deploy the BASE (the overlay ref is a local,
	// non-registry tag, so looksLikeFullRef can't protect it). R3: the same
	// resolveDeployResolvedImage gate guards both consume sites.
	usingResolvedOverlay := resolveDeployResolvedImage(c.Box, c.Instance) == imageRef && imageRef != ""
	if meta.Registry != "" && !kit.LooksLikeFullRef(imageRef) && c.ExplicitRef == "" && !usingResolvedOverlay {
		imageRef = resolveShellImageRef(meta.Registry, deployBoxName, c.Tag)
	}

	// Resolve tunnel config from labels
	var tunnelCfg *TunnelConfig
	if meta.Tunnel != nil {
		tunnelCfg = TunnelConfigFromMetadata(meta)
	}

	// Apply CLI --port overrides FIRST so env_provides templates that
	// reference {{.HostPort N}} see the final host-port mapping, not
	// the pre-override values. The post-2026-05 reorder put this block
	// BEFORE injectEnvProvides — previously these overrides applied
	// AFTER env injection, so {{.HostPort N}} substitutions would
	// resolve against pre-override ports (silent staleness when an
	// operator used `charly config -p NEW:CONT`).
	if len(c.Port) > 0 {
		var portErr error
		ports, portErr = ApplyPortOverrides(ports, c.Port)
		if portErr != nil {
			return portErr
		}
	}

	// Build the {containerPort -> hostPort} lookup table that the
	// inject functions pass into resolveTemplate for {{.HostPort N}}
	// substitution. nil-safe — if ports is empty the map is nil and
	// HostPort templates degrade to the literal container port.
	portMap := PortMapFromMappings(ports)

	// Inject provides BEFORE env resolution so this image's own provides
	// (pod case) and other images' provides are available in the quadlet.
	if len(meta.EnvProvide) > 0 {
		if _, injErr := injectEnvProvides(c.Box, c.Instance, meta.EnvProvide, portMap); injErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not inject env_provides: %v\n", injErr)
		}
	}
	if len(meta.MCPProvide) > 0 {
		if _, injErr := injectMCPProvides(c.Box, c.Instance, meta.MCPProvide, portMap); injErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not inject mcp_provides: %v\n", injErr)
		}
	}
	// Reload deploy config after injection to pick up newly injected provides
	dc = deploykit.LoadDeployConfigForRead("charly config reload-after-inject")

	// Resolve SSH key if --ssh-key was provided
	if c.SshKey != "" {
		cName := kit.ContainerNameInstance(c.Box, c.Instance)
		sshDir, sshDirErr := containerSSHKeyDir(cName)
		if sshDirErr != nil {
			return sshDirErr
		}
		pubkey, sshErr := resolveSSHPubKey(c.SshKey, sshDir)
		if sshErr != nil {
			return fmt.Errorf("resolving SSH key: %w", sshErr)
		}
		if pubkey != "" {
			c.Env = append(c.Env, "SSH_AUTHORIZED_KEYS="+pubkey)
		}
	}

	// Resolve env vars from global provides + labels + charly.yml + CLI.
	// Pass deployKey (box-with-instance) — NOT bare c.Box — so an
	// instance consumer like `versa/ecovoyage` doesn't pick up the base
	// `versa` deploy's provides, and vice versa.
	ctrName := kit.ContainerNameInstance(c.Box, c.Instance)
	acceptedEnv := AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
	globalEnv := dc.GlobalEnvForImage(deploykit.DeployKey(c.Box, c.Instance), ctrName, acceptedEnv)
	envVars, envErr := kit.ResolveEnvVars(globalEnv, meta.Env, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}
	envVars = kit.EnrichNoProxy(envVars, dc.DeployedContainerNames())

	// Enforce env_requires — hard error before writing anything
	if len(meta.EnvRequire) > 0 {
		if err := checkMissingEnvRequires(c.Box, meta.EnvRequire, envVars); err != nil {
			return err
		}
	}

	// For quadlet, resolve env file to absolute path for EnvironmentFile=
	// (CLI --env-file > charly.yml env_file > workspace .env).
	quadletEnvFile := c.prepareQuadletEnv(dc, bindMounts)

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = deploykit.AppendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	// Resolve network (default to shared "charly" network)
	resolvedNetwork, netErr := ResolveNetwork(network, rt.RunEngine)
	if netErr != nil {
		return netErr
	}

	// Port overrides already applied above (before env injection so
	// {{.HostPort N}} templates see the final mapping).

	// Pre-flight port conflict check (warning for config, not hard error)
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, rt.RunEngine); len(conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: port conflicts detected:%s", FormatPortConflicts(conflicts, c.Box))
	}

	// Collect and provision secrets from image labels.
	//
	// Two sources feed the provisioning step:
	//
	//  1. Candy-owned secrets (existing, unchanged): declared in
	//     the candy manifest `secrets:`, provisioned per-image, never rotated on
	//     config. Example: immich's db-password.
	//  2. Credential-store-backed secrets (new in this release): declared
	//     as secret_accepts/secret_requires on the candy manifest, synthesized from
	//     image labels + the credential store. Plan §2.1–2.3. RotateOnConfig
	//     is true so every charly config reconciles them with the latest
	//     credential store value.
	//
	// Both flow into the same ProvisionPodmanSecrets call — the existing
	// Secret=<name>,type=env,target=<var> emission at quadlet.go:100-106
	// handles them identically at runtime.
	candyOwnedSecrets := CollectSecretsFromLabels(c.Box, meta.Secret)
	credBackedSecrets, secretResolutions := CollectCandySecretAccepts(c.Box, c.Instance, meta)

	// Enforce secret_requires — hard error before writing anything. Runs
	// alongside checkMissingEnvRequires (handled later in env resolution).
	// Plan §2.6 / §6.6.
	if len(meta.SecretRequire) > 0 {
		if err := checkMissingSecretRequires(c.Box, meta.SecretRequire, secretResolutions); err != nil {
			return err
		}
	}

	collectedSecrets := append(slices.Clone(candyOwnedSecrets), credBackedSecrets...)
	collectedSecrets, unmatchedRefresh := ApplySecretRefresh(collectedSecrets, c.RefreshSecret)
	for _, name := range unmatchedRefresh {
		fmt.Fprintf(os.Stderr, "Warning: --refresh-secret %s matched no secret declared by %s\n", name, c.Box)
	}
	autoGen := c.Password == "auto"
	provisioned, fallbackEnv, err := ProvisionPodmanSecrets(rt.RunEngine, c.Box, c.Instance, collectedSecrets, autoGen)
	if err != nil {
		return err
	}
	for _, kv := range fallbackEnv {
		envVars = appendEnvUnique(envVars, kv)
	}

	// For quadlet, we use EnvironmentFile= instead of inline Environment= for file-sourced vars.
	// Only pass CLI -e vars as inline Environment= entries.
	charlyBin, _ := os.Executable()
	// Determine keyring backend from configured secret_backend setting, not runtime
	// probe. At boot or when keyring is locked, DefaultCredentialStore() may return
	// ConfigFileStore even though the user configured "keyring". The quadlet must
	// reflect the intended backend so TimeoutStartSec=0 and WantedBy are correct.
	backend := resolveSecretBackend()
	isKeyring := backend == "keyring" || backend == "auto" || backend == ""

	// Resolve sidecars (embedded templates + charly.yml + --sidecar flags),
	// route CLI -e flags to the matching sidecar, and provision sidecar secrets.
	deploySidecars, resolvedSidecars, sidecarEnv, scErr := c.resolveSidecars(dc, rt, autoGen, envVars)
	if scErr != nil {
		return scErr
	}
	envVars = sidecarEnv

	// When sidecars are present, set PodName to enable pod mode
	podName := ""
	if len(resolvedSidecars) > 0 {
		podName = kit.PodNameInstance(c.Box, c.Instance)
	}

	qcfg := deploykit.QuadletConfig{
		BoxName:         c.Box,
		ImageRef:        imageRef,
		Home:            meta.Home,
		Ports:           ports,
		Volumes:         volumes,
		BindMounts:      bindMounts,
		GPU:             detected.GPU || deployNodeSharesGPU(dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)], gatherResources()),
		BindAddress:     rt.BindAddress,
		Tunnel:          tunnelCfg,
		UID:             uid,
		GID:             gid,
		Env:             envVars,
		EnvFile:         quadletEnvFile,
		Instance:        c.Instance,
		Security:        security,
		Network:         resolvedNetwork,
		Status:          meta.Status,
		Info:            meta.Info,
		Entrypoint:      resolveEntrypointFromMeta(meta),
		Secrets:         provisioned,
		CharlyBin:       charlyBin,
		EncryptedMounts: hasEncryptedBindMounts(bindMounts),
		KeyringBackend:  isKeyring,
		PodName:         podName,
		Sidecar:         resolvedSidecars,
	}

	// Suppress file-sourced env vars if using EnvFile (avoid duplication).
	// Keep CLI -e flags + provides env vars + auto-detected env vars as inline env.
	// Provides vars (from env_provides) are NOT in the env file — they're resolved
	// at charly config time from charly.yml and must remain as inline Environment= entries.
	if quadletEnvFile != "" {
		qcfg.Env = append([]string{}, globalEnv...)
		qcfg.Env = append(qcfg.Env, c.Env...)
		qcfg.Env = appendAutoDetectedEnv(qcfg.Env, detected)
	}

	// Persist deployment state to charly.yml (source of truth).
	// SecretNames is passed as the defense-in-depth list that
	// saveDeployState uses to scrub any plaintext credential that slipped
	// through the Run() pipeline — see deploy.go:saveDeployState docstring.
	// Ports: write only when the operator passed --port flags this run.
	// Without SetPorts gating, `charly config <name>` (no flags) would
	// silently overwrite operator port overrides with the merged
	// `meta.Port` value, since `ports` is computed from image labels
	// merged with charly.yml — an idempotent recompute, not an explicit
	// operator decision to set ports.
	deploykit.SaveDeployState(c.Box, c.Instance, deploykit.SaveDeployStateInput{
		Ports:       ports,
		SetPorts:    len(c.Port) > 0,
		Env:         kit.EnvPairsToMap(c.Env),
		CleanEnv:    c.Clean,
		EnvFile:     quadletEnvFile,
		Network:     resolvedNetwork,
		Security:    &security,
		Volume:      deployVolumes,
		Sidecar:     deploySidecars,
		Tunnel:      meta.Tunnel,
		SecretNames: secretDepNames(meta),
		// Box + Target are required by the 2026-05-12 require-image
		// validator (validateDeployRequiresBox). Without them, the entry
		// `charly config` writes would be rejected by the loader on the next
		// `charly` invocation, forcing an `charly migrate`. saveDeployState only
		// writes these when the existing entry doesn't already declare them
		// (never clobbers operator-authored refs). charly config is exclusively
		// a pod-deploy setup verb, so Target is always "pod"; Box is the
		// RESOLVED box short-name (deployBoxName), NOT the deploy key —
		// the key and the box diverge for kind:check beds and Pattern-B
		// deploys. Mirrors the fields set by the container path in
		// deploy_add_cmd.go.
		Box:    deployBoxName,
		Target: "pod",
	}, marshalDeployNode)

	// Direct mode: skip quadlet+systemctl and run podman directly. Used
	// in nested environments (harness sandbox pods, supervisord-only containers,
	// sysvinit hosts) where systemd-user is unavailable. Sidecars,
	// encrypted volumes, and tunnel companion services require systemd
	// and are not supported in direct mode — the branch warns and
	// proceeds without those features.
	if rt.RunMode == "direct" {
		return c.runConfigDirect(qcfg, bindMounts, resolvedSidecars, tunnelCfg)
	}

	// The config-WRITE (quadlet/.pod/sidecar/tunnel file generation) is owned by the deploy:pod
	// plugin (P11, Ruling C). The HOST resolves the QuadletConfig (above), provisions the target
	// dirs, and computes the exact file PATHS (the core filename helpers), then Invokes the plugin
	// to render + write the file CONTENTS byte-identically. RESOLVE + the host side-effects below
	// (cloudflareTunnelSetup, systemctl, enc-mount, data-seed) stay here.
	qdir, err := kit.QuadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}
	writeReq := spec.PodConfigWriteRequest{
		ContainerPath: filepath.Join(qdir, kit.QuadletFilenameInstance(c.Box, c.Instance)),
	}
	if len(resolvedSidecars) > 0 {
		writeReq.PodPath = filepath.Join(qdir, kit.PodQuadletFilenameInstance(c.Box, c.Instance))
		writeReq.SidecarPaths = make(map[string]string, len(resolvedSidecars))
		for _, sc := range resolvedSidecars {
			writeReq.SidecarPaths[sc.Name] = filepath.Join(qdir, kit.SidecarQuadletFilenameInstance(c.Box, c.Instance, sc.Name))
		}
	}
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		svcDir, err := kit.SystemdUserDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(svcDir, 0755); err != nil {
			return fmt.Errorf("creating systemd user directory: %w", err)
		}
		writeReq.TunnelPath = filepath.Join(svcDir, deploykit.TunnelServiceFilename(c.Box))
		cfgPath, _ := tunnelConfigPath(tunnelCfg.TunnelName)
		writeReq.CloudflaredCfgPath = cfgPath
	}
	qcfgJSON, err := json.Marshal(qcfg)
	if err != nil {
		return fmt.Errorf("marshaling pod config for write: %w", err)
	}
	writeReq.PodConfigJSON = qcfgJSON
	writeReply, err := writePodConfigViaPlugin(writeReq)
	if err != nil {
		return fmt.Errorf("writing pod config files: %w", err)
	}
	for _, p := range writeReply.WrittenPaths {
		fmt.Fprintf(os.Stderr, "Wrote %s\n", p)
	}

	// Cloudflare tunnel setup (create tunnel, write cloudflared config, route DNS) — a HOST
	// side-effect that stays here (Q1=(a)); the plugin only WROTE the .service unit above.
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		if _, _, setupErr := cloudflareTunnelSetup(*tunnelCfg); setupErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", setupErr)
		}
	}

	// Clean up stale enc service from previous charly versions
	if svcDir, svcErr := kit.SystemdUserDir(); svcErr == nil {
		encPath := filepath.Join(svcDir, encServiceFilename(c.Box))
		if _, statErr := os.Stat(encPath); statErr == nil {
			_ = os.Remove(encPath)
			fmt.Fprintf(os.Stderr, "Removed stale %s\n", encPath)
		}
	}

	reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := reloadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")

	// Enable tunnel service so it auto-starts with the container
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		enableCmd := exec.Command("systemctl", "--user", "enable", deploykit.TunnelServiceFilename(c.Box))
		if output, err := enableCmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not enable tunnel service: %v\n%s", err, strings.TrimSpace(string(output)))
		}
	}

	// Initialize and mount encrypted volumes
	if hasEncryptedBindMounts(bindMounts) {
		if err := ensureEncryptedMounts(c.Box, c.Instance, autoGen); err != nil {
			return fmt.Errorf("setting up encrypted volumes: %w", err)
		}
		// Unmount after setup unless --keep-mounted
		if !c.KeepMounted {
			if err := encUnmount(c.Box, c.Instance, ""); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not unmount encrypted volumes: %v\n", err)
			}
		}
	}

	// Reload deploy config after saveDeployState wrote the volumes
	dc = deploykit.LoadDeployConfigForRead("charly config reload-after-volumes")

	// Provision data from image staging (/data/) into the image's volumes
	// (both bind mounts and named volumes — provisionData dispatches on kind).
	if c.Seed && (len(bindMounts) > 0 || len(volumes) > 0) {
		dataMeta := meta
		dataRef := imageRef
		dataEngine := ResolveBoxEngineForDeploy(c.Box, c.Instance, rt.RunEngine)

		// Use external data image if --data-from specified
		if c.DataFrom != "" {
			dataRef = c.DataFrom
			if !strings.Contains(dataRef, ":") {
				// Short name without tag — resolve to newest local CalVer
				// (charly is CalVer-only; no `:latest` fallback).
				if resolved, err := kit.ResolveNewestLocalCalVer(dataEngine, dataRef); err == nil && resolved != "" {
					dataRef = resolved
				}
			}
			dm, err := ExtractMetadata(dataEngine, dataRef)
			if err != nil {
				return fmt.Errorf("extracting metadata from data image %s: %w", dataRef, err)
			}
			if dm == nil {
				return fmt.Errorf("data image %s has no embedded metadata", dataRef)
			}
			dataMeta = dm
		}

		if len(dataMeta.DataEntries) > 0 {
			// Determine provisioning mode
			mode := DataProvisionInitial
			if c.ForceSeed {
				mode = DataProvisionForce
			} else {
				// Check if already seeded (idempotent re-run)
				allSeeded := true
				for _, dvc := range deployVolumes {
					if dvc.DataSeeded {
						continue
					}
					allSeeded = false
					break
				}
				if allSeeded && len(deployVolumes) > 0 && !c.ForceSeed {
					// Skip if all volumes already seeded and no force
					fmt.Fprintln(os.Stderr, "Data already provisioned (use --force-seed to re-provision)")
					goto skipDataProvision
				}
			}

			fmt.Fprintln(os.Stderr, "Provisioning data into volumes...")
			seeded, err := provisionData(dataEngine, dataRef, dataMeta, bindMounts, volumes, c.Box, c.Instance, mode)
			if err != nil {
				return fmt.Errorf("data provisioning: %w", err)
			}

			// Update charly.yml with seeded state
			if seeded > 0 {
				if dc == nil {
					dc = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
				}
				imgDeploy := dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)]
				for i := range imgDeploy.Volume {
					for _, entry := range dataMeta.DataEntries {
						if imgDeploy.Volume[i].Name == entry.Volume {
							imgDeploy.Volume[i].DataSeeded = true
							imgDeploy.Volume[i].DataSource = dataRef
						}
					}
				}
				dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)] = imgDeploy
				if err := saveBundleConfigNodeForm(dc); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not save data seeded state to charly.yml: %v\n", err)
				}
				fmt.Fprintf(os.Stderr, "Provisioned data for %d volume(s)\n", seeded)
			}
		}
	}
skipDataProvision:

	// Run post_enable hooks from image labels
	hooks := meta.Hook
	if hooks != nil && hooks.PostEnable != "" {
		ctrName := kit.ContainerNameInstance(c.Box, c.Instance)
		svc := kit.ServiceNameInstance(c.Box, c.Instance)

		start := exec.Command("systemctl", "--user", "start", svc)
		start.Stdout = os.Stderr
		start.Stderr = os.Stderr
		if err := start.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start %s for post_enable hook: %v\n", svc, err)
		} else {
			engine := kit.EngineBinary(rt.RunEngine)
			// Pass credential-backed secrets (secret_accept/require) to the hook
			// explicitly — they're scrubbed from c.Env and not reliably inherited
			// from the container's type=env secrets by `podman exec`.
			hookEnv := append(append([]string{}, c.Env...), resolveHookSecretEnv(c.Box, c.Instance, meta)...)
			if err := RunHook(engine, ctrName, hooks.PostEnable, hookEnv); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: post_enable hook failed: %v\n", err)
			}
		}
	}

	// Regenerate quadlets for all other deployed images if --update-all
	if c.UpdateAll {
		if err := updateAllDeployedQuadlets(rt, deploykit.DeployKey(c.Box, c.Instance)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update all quadlets: %v\n", err)
		}
	}

	// Warn about missing mcp_requires servers
	if len(meta.MCPRequire) > 0 {
		dc := deploykit.LoadDeployConfigForRead("charly config mcp_requires check")
		var mcpServers []MCPProvideEntry
		if dc != nil && dc.Provides != nil {
			mcpServers = spec.PodAwareMCPProvides(dc.Provides.MCP, deploykit.DeployKey(c.Box, c.Instance), kit.ContainerNameInstance(c.Box, c.Instance))
		}
		warnMissingMCPRequires(c.Box, meta.MCPRequire, mcpServers)
	}

	return nil
}

// directDeployMarker is the on-disk record of a direct-mode deploy. Used
// by lifecycle commands (start/stop/status/logs/remove) to detect that a
// deploy was created without quadlet so they should talk to podman
// directly instead of `systemctl --user`.
type directDeployMarker struct {
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	Instance      string `json:"instance,omitempty"`
	ImageRef      string `json:"image_ref"`
	CreatedUTC    string `json:"created_utc"`
}

// directDeployMarkerDir returns ~/.config/charly/direct/, the registry
// directory for direct-mode deploys (the equivalent of
// ~/.config/containers/systemd/ for quadlet deploys).
func directDeployMarkerDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home: %w", err)
	}
	return filepath.Join(home, ".config", "charly", "direct"), nil
}

// directDeployMarkerPath returns the marker JSON path for a deploy.
func directDeployMarkerPath(box, instance string) (string, error) {
	dir, err := directDeployMarkerDir()
	if err != nil {
		return "", err
	}
	name := kit.ContainerNameInstance(box, instance)
	return filepath.Join(dir, name+".json"), nil
}

// IsDirectDeploy reports whether the named deploy was created in
// direct mode (i.e. has a marker file). Used by lifecycle commands.
func IsDirectDeploy(box, instance string) bool {
	path, err := directDeployMarkerPath(box, instance)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// writeDirectDeployMarker persists the marker JSON.
func writeDirectDeployMarker(m directDeployMarker) error {
	path, err := directDeployMarkerPath(m.Image, m.Instance)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating direct-mode marker dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// removeDirectDeployMarker removes the marker file (used by `charly remove`).
func removeDirectDeployMarker(box, instance string) error {
	path, err := directDeployMarkerPath(box, instance)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// directPodmanArgs translates a QuadletConfig + bind mounts into the
// `podman run -d ...` argv. Each translation maps 1:1 to the equivalent
// quadlet directive (see plan G.2 translation table); changes here
// should match the corresponding generateQuadlet field handling.
func directPodmanArgs(qcfg deploykit.QuadletConfig, bindMounts []deploykit.ResolvedBindMount) []string {
	name := kit.ContainerNameInstance(qcfg.BoxName, qcfg.Instance)
	args := []string{"run", "-d",
		"--name", name,
		"--hostname", name,
		"--restart=always",
	}
	if qcfg.Network != "" {
		args = append(args, "--network", qcfg.Network)
	} else {
		args = append(args, "--network", "charly")
	}
	for _, p := range qcfg.Ports {
		// PublishPort lines map to -p directly. Bind address prefix if
		// the qcfg has one (matches generateQuadlet behavior).
		if qcfg.BindAddress != "" && qcfg.BindAddress != "0.0.0.0" {
			args = append(args, "-p", qcfg.BindAddress+":"+p)
		} else {
			args = append(args, "-p", p)
		}
	}
	for _, v := range qcfg.Volumes {
		// Named volume — podman manages backing.
		args = append(args, "-v", v.VolumeName+":"+v.ContainerPath)
	}
	for _, bm := range bindMounts {
		// Host bind mount (or encrypted plain dir).
		args = append(args, "-v", bm.HostPath+":"+bm.ContPath)
	}
	for _, e := range qcfg.Env {
		args = append(args, "-e", e)
	}
	if qcfg.EnvFile != "" {
		args = append(args, "--env-file", qcfg.EnvFile)
	}
	// Translate security config to podman flags via the existing
	// SecurityArgs helper (the same source quadlet uses).
	args = append(args, deploykit.SecurityArgs(qcfg.Security)...)
	// User-namespace mapping for bind-backed volumes (matches quadlet
	// behavior: keep-id when there are host bind mounts).
	if len(bindMounts) > 0 && qcfg.UID > 0 {
		args = append(args, "--userns",
			fmt.Sprintf("keep-id:uid=%d,gid=%d", qcfg.UID, qcfg.GID))
	}
	// Image ref.
	args = append(args, qcfg.ImageRef)
	// Entrypoint cmdline if set.
	args = append(args, qcfg.Entrypoint...)
	return args
}

// runConfigDirect is the direct-podman counterpart to the quadlet write
// path in runConfig. Skips quadlet generation, systemctl daemon-reload,
// post_enable hooks, encrypted-volume scope-unit setup, and tunnel
// companion services. Writes a marker file so lifecycle commands
// (start/stop/status/logs/remove) can route to podman instead of
// systemctl.
func (c *BoxConfigSetupCmd) runConfigDirect(
	qcfg deploykit.QuadletConfig,
	bindMounts []deploykit.ResolvedBindMount,
	sidecars []deploykit.ResolvedSidecar,
	tunnelCfg *TunnelConfig,
) error {
	if len(sidecars) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: sidecars are not supported in direct mode (skipping); use run_mode=quadlet for sidecar deploys.\n")
	}
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		fmt.Fprintf(os.Stderr, "Warning: cloudflare tunnel companion service requires systemd; tunnel will not be started in direct mode.\n")
	}
	if hasEncryptedBindMounts(bindMounts) {
		fmt.Fprintf(os.Stderr, "Warning: encrypted bind mounts require systemd-run; encrypted volumes will not be initialized in direct mode.\n")
	}

	name := kit.ContainerNameInstance(qcfg.BoxName, qcfg.Instance)
	// Idempotent re-deploy: stop + remove any existing container with the
	// same name. Errors are best-effort — if the container doesn't exist,
	// `podman rm` returns non-zero and we ignore it.
	_ = exec.Command("podman", "stop", name).Run()
	_ = exec.Command("podman", "rm", "-f", name).Run()

	args := directPodmanArgs(qcfg, bindMounts)
	cmd := exec.Command("podman", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman run %s failed: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	cid := strings.TrimSpace(string(out))
	fmt.Fprintf(os.Stderr, "Started %s (direct mode, container=%s)\n", name, cid[:12])

	// Persist marker for lifecycle commands.
	if err := writeDirectDeployMarker(directDeployMarker{
		ContainerName: name,
		Image:         qcfg.BoxName,
		Instance:      qcfg.Instance,
		ImageRef:      qcfg.ImageRef,
		CreatedUTC:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write direct-mode marker: %v\n", err)
	}
	return nil
}

// BoxConfigStatusCmd shows encrypted volume status.
type BoxConfigStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigStatusCmd) Run() error {
	return encStatus(c.Box, c.Instance)
}

// BoxConfigMountCmd mounts encrypted volumes.
type BoxConfigMountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only mount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigMountCmd) Run() error {
	return encMount(c.Box, c.Instance, c.Volume)
}

// BoxConfigUnmountCmd unmounts encrypted volumes.
type BoxConfigUnmountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only unmount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigUnmountCmd) Run() error {
	return encUnmount(c.Box, c.Instance, c.Volume)
}

// BoxConfigPasswdCmd changes the gocryptfs password.
type BoxConfigPasswdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigPasswdCmd) Run() error {
	return encPasswd(c.Box, c.Instance)
}

// BoxConfigRemoveCmd removes a quadlet service (replaces charly disable).
type BoxConfigRemoveCmd struct {
	Box      string `arg:"" help:"Box name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigRemoveCmd) Run() error {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	boxName := resolveBoxName(c.Box)

	// Direct-mode removal: the deploy was created without a quadlet, so
	// `systemctl --user disable` would fail. Stop + remove the container
	// directly via podman, then drop the marker file.
	if rt.RunMode == "direct" || IsDirectDeploy(boxName, c.Instance) {
		name := kit.ContainerNameInstance(boxName, c.Instance)
		_ = exec.Command("podman", "stop", name).Run()
		if out, err := exec.Command("podman", "rm", "-f", name).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: podman rm %s: %v\n%s", name, err, strings.TrimSpace(string(out)))
		}
		if err := removeDirectDeployMarker(boxName, c.Instance); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing direct-mode marker: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s (direct mode)\n", name)
		return nil
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("charly config remove requires run_mode=quadlet or direct (current: %s)", rt.RunMode)
	}

	svc := kit.ServiceNameInstance(boxName, c.Instance)
	cmd := exec.Command("systemctl", "--user", "disable", "--now", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	// Also disable pod and sidecar services (best-effort)
	podSvc := kit.PodNameInstance(boxName, c.Instance) + "-pod.service"
	disablePod := exec.Command("systemctl", "--user", "disable", "--now", podSvc)
	_ = disablePod.Run()

	// Disable sidecar services attached to this pod. Identified by the
	// `Pod=<podname>.pod` directive inside each quadlet's [Container]
	// section — the load-bearing invariant that distinguishes a true
	// sidecar from a sibling instance of the same image. A bare prefix
	// match on the filename collides with sibling instances (the
	// `<base>/<instance>` deploy-key pattern produces quadlet filenames
	// like charly-versa-ecovoyage.container that share the charly-versa- prefix
	// with sidecars but belong to an unrelated deploy). See
	// findPodSidecarQuadlets in sidecar.go.
	if qdir, qErr := kit.QuadletDir(); qErr == nil {
		podName := kit.PodNameInstance(boxName, c.Instance)
		mainFile := kit.ContainerNameInstance(boxName, c.Instance) + ".container"
		if sidecars, dErr := findPodSidecarQuadlets(qdir, podName, mainFile); dErr == nil {
			for _, name := range sidecars {
				scSvc := strings.TrimSuffix(name, ".container") + ".service"
				fmt.Fprintf(os.Stderr, "Disabling sidecar %s\n", scSvc)
				_ = exec.Command("systemctl", "--user", "disable", "--now", scSvc).Run()
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Disabled %s\n", svc)
	return nil
}

// parseVolumeFlags converts --volume, --bind, --encrypt flags into DeployVolumeConfig.
func (c *BoxConfigSetupCmd) parseVolumeFlags() []DeployVolumeConfig {
	var configs []DeployVolumeConfig
	seen := make(map[string]bool)

	// Parse --volume name:type[:path]
	for _, v := range c.VolumeFlag {
		parts := strings.SplitN(v, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		// Normalize: accept both "encrypt" and "encrypted"
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		if !seen[dv.Name] {
			configs = append(configs, dv)
			seen[dv.Name] = true
		}
	}

	// Parse --bind name or name=path
	for _, b := range c.Bind {
		if seen[b] || seen[strings.SplitN(b, "=", 2)[0]] {
			continue
		}
		if before, after, ok := strings.Cut(b, "="); ok {
			name := before
			host := after
			configs = append(configs, DeployVolumeConfig{Name: name, Type: "bind", Host: host})
			seen[name] = true
		} else {
			configs = append(configs, DeployVolumeConfig{Name: b, Type: "bind"})
			seen[b] = true
		}
	}

	// Parse --encrypt name
	for _, e := range c.Encrypt {
		if !seen[e] {
			configs = append(configs, DeployVolumeConfig{Name: e, Type: "encrypted"})
			seen[e] = true
		}
	}

	return configs
}

// persistResourceCaps writes the --memory-max/--memory-high/--memory-swap-max/--cpus
// flags (when provided) into charly.yml under this image's Security block. On
// subsequent runs MergeDeployOntoMetadata picks them up automatically — no other
// code path needs to know about the flags.
func (c *BoxConfigSetupCmd) persistResourceCaps(dc **deploykit.BundleConfig) error {
	if c.MemoryMax == "" && c.MemoryHigh == "" && c.MemorySwapMax == "" && c.Cpus == "" {
		return nil
	}
	if *dc == nil {
		*dc = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	}
	if (*dc).Bundle == nil {
		(*dc).Bundle = make(map[string]spec.BundleNode)
	}
	key := deploykit.DeployKey(c.Box, c.Instance)
	entry := (*dc).Bundle[key]
	if entry.Security == nil {
		entry.Security = &SecurityConfig{}
	}
	if c.MemoryMax != "" {
		entry.Security.MemoryMax = c.MemoryMax
	}
	if c.MemoryHigh != "" {
		entry.Security.MemoryHigh = c.MemoryHigh
	}
	if c.MemorySwapMax != "" {
		entry.Security.MemorySwapMax = c.MemorySwapMax
	}
	if c.Cpus != "" {
		entry.Security.Cpus = c.Cpus
	}
	(*dc).Bundle[key] = entry
	return saveBundleConfigNodeForm(*dc)
}

// parseVolumeEnv parses CHARLY_VOLUMES_<IMAGE> env var into DeployVolumeConfig.
func parseVolumeEnv(boxName string) []DeployVolumeConfig {
	envVarName := "CHARLY_VOLUMES_" + strings.ToUpper(strings.ReplaceAll(boxName, "-", "_"))
	envVal := os.Getenv(envVarName)
	if envVal == "" {
		return nil
	}

	var configs []DeployVolumeConfig
	for entry := range strings.SplitSeq(envVal, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		configs = append(configs, dv)
	}
	return configs
}

// injectEnvProvides resolves env_provides templates and stores them in charly.yml provides.env.
// Returns true if any env vars were added or changed.
//
// portMap is a {containerPort -> hostPort} lookup used by resolveTemplate
// to substitute {{.HostPort N}} placeholders against the resolved port
// mapping list. nil is accepted (HostPort substitutions degrade to the
// literal container port — only safe for candies that don't actually use
// the placeholder).
func injectEnvProvides(boxName, instance string, envProvides map[string]string, portMap map[int]int) (bool, error) {
	if len(envProvides) == 0 {
		return false, nil
	}

	dc, err := deploykit.LoadDeployConfigForWrite("injectEnvProvides")
	if err != nil {
		return false, err
	}
	if dc.Provides == nil {
		dc.Provides = &deploykit.ProvidesConfig{}
	}

	ctrName := kit.ContainerNameInstance(boxName, instance)
	changed := false

	// Sort keys for deterministic output
	keys := sortedStringMapKeys(envProvides)
	for _, key := range keys {
		tmpl := envProvides[key]
		value := resolveTemplate(tmpl, ctrName, portMap)
		source := deploykit.DeployKey(boxName, instance)
		resolved := deploykit.EnvProvideEntry{
			Name:   key,
			Value:  value,
			Source: source,
		}

		// Check if already set to same value (dedup by name+source)
		found := false
		for i, existing := range dc.Provides.Env {
			if existing.Name == key && existing.Source == source {
				if existing.Value == value {
					found = true
					break
				}
				dc.Provides.Env[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.Env = append(dc.Provides.Env, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "Env provides injected: %s=%s\n", key, value)
		}
	}

	if changed {
		if err := saveBundleConfigNodeForm(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// injectMCPProvides resolves mcp_provides templates and adds them to charly.yml.
// Returns true if any servers were added or changed.
//
// portMap is a {containerPort -> hostPort} lookup used by resolveTemplate
// to substitute {{.HostPort N}} placeholders. nil is accepted.
func injectMCPProvides(boxName, instance string, mcpProvides []spec.MCPServerYAML, portMap map[int]int) (bool, error) {
	if len(mcpProvides) == 0 {
		return false, nil
	}

	dc, err := deploykit.LoadDeployConfigForWrite("injectMCPProvides")
	if err != nil {
		return false, err
	}
	if dc.Provides == nil {
		dc.Provides = &deploykit.ProvidesConfig{}
	}

	ctrName := kit.ContainerNameInstance(boxName, instance)
	source := deploykit.DeployKey(boxName, instance)
	changed := false

	// Remove stale entries from this source (handles name changes on re-config)
	var cleaned []MCPProvideEntry
	for _, e := range dc.Provides.MCP {
		if e.Source != source {
			cleaned = append(cleaned, e)
		}
	}
	if len(cleaned) != len(dc.Provides.MCP) {
		dc.Provides.MCP = cleaned
	}

	for _, mcp := range mcpProvides {
		url := resolveTemplate(mcp.URL, ctrName, portMap)
		transport := mcp.Transport
		if transport == "" {
			transport = "http"
		}
		// Disambiguate MCP name for instances so consumers see unique servers
		mcpName := mcp.Name
		if instance != "" {
			mcpName = mcp.Name + "-" + instance
		}
		resolved := MCPProvideEntry{
			Name:      mcpName,
			URL:       url,
			Transport: transport,
			Source:    source,
		}

		// Check if already set to same value
		found := false
		for i, existing := range dc.Provides.MCP {
			if existing.Name == mcpName && existing.Source == source {
				if existing.URL == resolved.URL && existing.Transport == resolved.Transport {
					found = true
					break
				}
				dc.Provides.MCP[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.MCP = append(dc.Provides.MCP, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "MCP provides injected: %s → %s\n", mcpName, url)
		}
	}

	if changed {
		if err := saveBundleConfigNodeForm(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// warnMissingMCPRequires checks resolved MCP servers against required MCP dependencies
// and prints warnings for any that are missing.
func warnMissingMCPRequires(boxName string, requires []spec.EnvDependency, mcpServers []MCPProvideEntry) {
	resolved := make(map[string]bool, len(mcpServers))
	for _, s := range mcpServers {
		resolved[s.Name] = true
	}
	for _, dep := range requires {
		if !resolved[dep.Name] {
			desc := dep.Description
			if desc != "" {
				desc = " (" + desc + ")"
			}
			fmt.Fprintf(os.Stderr, "Warning: %s requires MCP server %s%s — not available\n", boxName, dep.Name, desc)
		}
	}
}

// checkMissingEnvRequires checks resolved env vars against required env dependencies.
// Returns an error if any required vars are missing — charly config will abort.
func checkMissingEnvRequires(boxName string, requires []spec.EnvDependency, resolvedEnv []string) error {
	// Build set of resolved env var names
	resolved := make(map[string]bool, len(resolvedEnv))
	for _, e := range resolvedEnv {
		if k := deploykit.EnvKey(e); k != "" {
			resolved[k] = true
		}
	}

	var missing []spec.EnvDependency
	for _, dep := range requires {
		if !resolved[dep.Name] {
			missing = append(missing, dep)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nError: %s requires the following environment variable(s):\n\n", boxName)
	for _, dep := range missing {
		desc := ""
		if dep.Description != "" {
			desc = " — " + dep.Description
		}
		fmt.Fprintf(os.Stderr, "  %s%s\n", dep.Name, desc)
	}
	fmt.Fprintf(os.Stderr, "\nSet them with -e flags, --env-file, or charly.yml env:\n\n")
	fmt.Fprintf(os.Stderr, "  charly config %s", boxName)
	for _, dep := range missing {
		fmt.Fprintf(os.Stderr, " -e %s=...", dep.Name)
	}
	fmt.Fprintf(os.Stderr, "\n\n")
	return fmt.Errorf("missing required environment variable(s) for %s", boxName)
}

// checkMissingSecretRequires reports a hard-fail when any secret_requires
// entry could not be resolved from the credential store. Parallel to
// checkMissingEnvRequires, but operates on the SecretResolution list
// produced by CollectCandySecretAccepts (which already has source
// classification) rather than a post-resolution env slice.
//
// The error message tells the user exactly which credential store path to
// populate, following the `charly secrets set charly/<service>/<key> <value>` form.
func checkMissingSecretRequires(boxName string, requires []spec.EnvDependency, resolutions []SecretResolution) error {
	resolvedByName := make(map[string]bool, len(resolutions))
	for _, r := range resolutions {
		if r.Resolved {
			resolvedByName[r.Name] = true
		}
	}

	var missing []spec.EnvDependency
	for _, dep := range requires {
		if !resolvedByName[dep.Name] {
			missing = append(missing, dep)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nError: %s requires the following credential-backed secret(s):\n\n", boxName)
	for _, dep := range missing {
		desc := ""
		if dep.Description != "" {
			desc = " — " + dep.Description
		}
		fmt.Fprintf(os.Stderr, "  %s%s\n", dep.Name, desc)
	}
	fmt.Fprintf(os.Stderr, "\nStore them in the credential backend. For each entry:\n\n")
	for _, dep := range missing {
		service, key := secretKeyForDep(dep)
		fmt.Fprintf(os.Stderr, "  charly secrets set %s %s <value>\n", service, key)
	}
	fmt.Fprintf(os.Stderr, "\nAlternatively, pass the value once via -e; it will be auto-imported:\n\n")
	fmt.Fprintf(os.Stderr, "  charly config %s", boxName)
	for _, dep := range missing {
		fmt.Fprintf(os.Stderr, " -e %s=...", dep.Name)
	}
	fmt.Fprintf(os.Stderr, "\n\n")
	return fmt.Errorf("missing required credential-backed secret(s) for %s", boxName)
}

// updateAllDeployedQuadlets regenerates quadlets for all other deployed images
// to pick up global env changes. Lightweight: only regenerates the quadlet file,
// does NOT re-provision secrets, encrypted volumes, or data.
//
//nolint:gocyclo // per-deploy quadlet-rewrite loop; each step (load metadata → merge deploy config → resolve env → rewrite quadlet) is a peer; extraction needs unwieldy param passing
func updateAllDeployedQuadlets(rt *kit.ResolvedRuntime, skipBox string) error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return nil
	}

	var updated []string
	for key := range dc.Bundle {
		if key == skipBox {
			continue
		}
		boxName, instance := deploykit.ParseDeployKey(key)

		// Check if quadlet file exists (only update deployed images)
		qdir, err := kit.QuadletDir()
		if err != nil {
			continue
		}
		qpath := filepath.Join(qdir, kit.QuadletFilenameInstance(boxName, instance))
		if _, err := os.Stat(qpath); os.IsNotExist(err) {
			continue
		}

		// Image ref: PREFER the existing quadlet's Image= line over a
		// fresh resolveShellImageRef("", boxName, "") lookup. The
		// fresh lookup walks all local images that carry the matching
		// ai.opencharly.image label, which includes per-deploy alias
		// re-tags (tagDeployAlias in deploy_target_pod.go). When
		// a sibling deploy of the same image has just been charly-updated
		// (e.g. an check bed of the versa image), its alias tag
		// (`<registry>/<sibling-deploy>:<calver>`) inherits the same
		// labels as the base, so the fresh lookup can — and did, see
		// the 2026-05-26 bug — pick the SIBLING's alias instead of the
		// bare base ref. updateAllDeployedQuadlets's documented purpose
		// is to refresh the Environment= block to pick up env_provides
		// changes; it should NEVER overwrite the operator's deliberate
		// Image= choice on an unrelated deploy. Preserving the existing
		// line is the correct fix at the cross-deploy refresh boundary.
		// Use `charly update <deploy>` (which routes through the pod substrate's
		// lifecycle-hook Rebuild — deploy add → config → start, optionally
		// `--build`) to actually advance a deploy's image — that path is the
		// operator-authorized way to move tags.
		imageRef, _ := extractQuadletImageLine(qpath)
		if imageRef == "" {
			imageRef = resolveShellImageRef("", boxName, "")
		}
		meta, err := ExtractMetadata("podman", imageRef)
		if err != nil || meta == nil {
			fmt.Fprintf(os.Stderr, "Warning: could not read metadata for %s, skipping quadlet update\n", key)
			continue
		}

		// Apply charly.yml overrides (instance-aware). Key by the deploy-key
		// base (boxName from parseDeployKey), not meta.Box — a bed /
		// Pattern-B entry carries a key distinct from its baked image label.
		deploykit.MergeDeployOntoMetadata(meta, dc, boxName, instance)

		// Resolve env vars with updated global env. Pass deployKey so an
		// instance's quadlet doesn't pick up another instance's provides.
		updateCtrName := kit.ContainerNameInstance(boxName, instance)
		updateAccepted := AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
		globalEnv := dc.GlobalEnvForImage(deploykit.DeployKey(boxName, instance), updateCtrName, updateAccepted)
		envVars, err := kit.ResolveEnvVars(globalEnv, meta.Env, "", "", "", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not resolve env for %s: %v\n", key, err)
			continue
		}
		envVars = kit.EnrichNoProxy(envVars, dc.DeployedContainerNames())

		// Resolve network
		resolvedNetwork, _ := ResolveNetwork(meta.Network, rt.RunEngine)

		// Detect devices for GPU config
		detected := DetectHostDevices()

		// Build volumes from metadata
		var deployVolumes []DeployVolumeConfig
		var deploySidecars map[string]json.RawMessage
		if overlay, ok := dc.Bundle[key]; ok {
			deployVolumes = overlay.Volume
			deploySidecars = overlay.Sidecar
		}
		volumes, bindMounts := deploykit.ResolveVolumeBacking(boxName, instance, meta.Volume, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

		// Resolve env file
		var quadletEnvFile string
		if overlay, ok := dc.Bundle[key]; ok && overlay.EnvFile != "" {
			quadletEnvFile = kit.ExpandHostHome(overlay.EnvFile)
		}
		if quadletEnvFile == "" {
			if wsHost := workspaceBindHost(bindMounts); wsHost != "" {
				wsEnvPath := filepath.Join(wsHost, ".env")
				if _, statErr := os.Stat(wsEnvPath); statErr == nil {
					quadletEnvFile = wsEnvPath
				}
			}
		}

		// Merge security
		security := meta.Security
		if !security.Privileged {
			security.Devices = deploykit.AppendUnique(security.Devices, detected.Devices...)
			if detected.AMDGPU {
				security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
			}
		}
		envVars = appendAutoDetectedEnv(envVars, detected)

		// Collect secrets from labels (for quadlet Secret= directives).
		//
		// Two sources: candy-owned secrets from meta.Secret (existing, unchanged)
		// and credential-backed secrets synthesized from meta.SecretAccept /
		// meta.SecretRequire (new in the credential-backed-secrets feature).
		// Both flow through the same cfg.Secrets slice and the same Secret=
		// emission at quadlet.go:100-106.
		//
		// This mirrors the Run() flow exactly. Without this merge, --update-all
		// regenerations would drop credential-backed Secret= directives from
		// consumer quadlets, causing `secret_requires` entrypoints to crashloop
		// on missing env vars. Plan §2.3. See regression caught during the
		// live-system testing session: charly-openwebui went FATAL after an
		// `charly config immich-ml --update-all` wiped its credential Secret= lines.
		provisioned := CollectSecretsFromLabels(boxName, meta.Secret)
		credBacked, credResolutions := CollectCandySecretAccepts(boxName, instance, meta)
		provisioned = append(provisioned, credBacked...)

		// Mirror Run()'s checkMissingSecretRequires — but downgrade to a
		// warning instead of a hard error, because --update-all should not
		// abort the regeneration of unrelated quadlets just because one
		// consumer is missing a required credential. The consumer will
		// crashloop on restart if the value is truly missing, which is the
		// user-visible signal. For secret_requires this is strictly
		// informational.
		if len(meta.SecretRequire) > 0 {
			missing := 0
			for _, r := range credResolutions {
				if r.Required && !r.Resolved {
					missing++
				}
			}
			if missing > 0 {
				fmt.Fprintf(os.Stderr, "Warning: %s has %d unresolved secret_requires entries (quadlet regenerated; image may crashloop on restart)\n", key, missing)
			}
		}

		charlyBin, _ := os.Executable()
		backend := resolveSecretBackend()
		isKeyring := backend == "keyring" || backend == "auto" || backend == ""

		// NOTE: previously this block re-resolved imageRef via
		// resolveShellImageRef(meta.Registry, boxName, "") — i.e. it
		// overwrote the operator-chosen Image= line at the LAST minute
		// before emitting the quadlet. That fresh resolution is the
		// exact cross-pollution path described in the comment near the
		// extractQuadletImageLine call earlier in this function. We
		// keep the existing-quadlet-derived imageRef unchanged here so
		// the operator's deliberate Image= choice survives the env-
		// refresh pass intact.

		// Resolve tunnel config from metadata (includes charly.yml overrides)
		var tunnelCfg *TunnelConfig
		if meta.Tunnel != nil {
			tunnelCfg = TunnelConfigFromMetadata(meta)
		}

		// Resolve sidecars from charly.yml for pod mode (regeneration path — no CLI
		// env to route; candy/plugin-sidecar merges + resolves the persisted overrides).
		var resolvedSidecars []deploykit.ResolvedSidecar
		podName := ""
		if len(deploySidecars) > 0 {
			embedded, embErr := embeddedSidecarBodies()
			if embErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: resolving sidecars for %s: %v\n", key, embErr)
				continue
			}
			reply, resolveErr := resolveSidecarsViaPlugin(spec.SidecarResolveInput{
				EmbeddedTemplates: embedded,
				ProjectTemplates:  sidecarTemplatesOf(dc),
				DeployOverrides:   deploySidecars,
				Box:               boxName,
				Instance:          instance,
			})
			if resolveErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: resolving sidecars for %s: %v\n", key, resolveErr)
				continue
			}
			if len(reply.Sidecars) > 0 {
				for _, rs := range reply.Sidecars {
					resolvedSidecars = append(resolvedSidecars, resolvedSidecarFromSpec(rs))
				}
				podName = kit.PodNameInstance(boxName, instance)
			}
		}

		qcfg := deploykit.QuadletConfig{
			BoxName:         boxName,
			Instance:        instance,
			ImageRef:        imageRef,
			Home:            meta.Home,
			Ports:           meta.Port,
			Volumes:         volumes,
			BindMounts:      bindMounts,
			GPU:             detected.GPU || deployNodeSharesGPU(dc.Bundle[key], gatherResources()),
			BindAddress:     rt.BindAddress,
			Tunnel:          tunnelCfg,
			UID:             meta.UID,
			GID:             meta.GID,
			Env:             envVars,
			EnvFile:         quadletEnvFile,
			Security:        security,
			Network:         resolvedNetwork,
			Status:          meta.Status,
			Info:            meta.Info,
			Entrypoint:      resolveEntrypointFromMeta(meta),
			Secrets:         provisioned,
			CharlyBin:       charlyBin,
			EncryptedMounts: hasEncryptedBindMounts(bindMounts),
			KeyringBackend:  isKeyring,
			PodName:         podName,
			Sidecar:         resolvedSidecars,
		}

		// Suppress file-sourced env vars if using EnvFile.
		// Keep provides env vars — they're not in the env file.
		if quadletEnvFile != "" {
			qcfg.Env = append([]string{}, globalEnv...)
			qcfg.Env = appendAutoDetectedEnv(qcfg.Env, detected)
		}

		// Config-WRITE via the deploy:pod plugin (P11) — the host computed the paths (qpath +
		// pod/sidecar) + Invokes the plugin to render + write the file contents. Best-effort per the
		// --update-all contract: a write failure warns + moves to the next deploy.
		writeReq := spec.PodConfigWriteRequest{ContainerPath: qpath}
		if len(resolvedSidecars) > 0 {
			writeReq.PodPath = filepath.Join(qdir, kit.PodQuadletFilenameInstance(boxName, instance))
			writeReq.SidecarPaths = make(map[string]string, len(resolvedSidecars))
			for _, sc := range resolvedSidecars {
				writeReq.SidecarPaths[sc.Name] = filepath.Join(qdir, kit.SidecarQuadletFilenameInstance(boxName, instance, sc.Name))
			}
		}
		qcfgJSON, err := json.Marshal(qcfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not marshal config for %s: %v\n", key, err)
			continue
		}
		writeReq.PodConfigJSON = qcfgJSON
		if _, err := writePodConfigViaPlugin(writeReq); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update quadlet for %s: %v\n", key, err)
			continue
		}

		updated = append(updated, key)
		fmt.Fprintf(os.Stderr, "Updated quadlet for %s\n", key)
	}

	if len(updated) > 0 {
		reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
		if output, err := reloadCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
		fmt.Fprintf(os.Stderr, "Restart affected services to pick up changes\n")
	}

	return nil
}

// sortedStringMapKeys returns the keys of a string map in sorted order.
func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
