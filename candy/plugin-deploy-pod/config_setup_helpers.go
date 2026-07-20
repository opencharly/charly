package deploypod

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// config_setup_helpers.go — pure (no host/loader/registry coupling) helpers ported VERBATIM from
// the former charly-core config_image.go, alongside the small "one seam bundles several pure
// calls" plugin-local glue (sidecarTemplatesOf, secretDepNames, secretResolution).

// sidecarTemplatesOf mirrors charly/sidecar.go's sidecarTemplatesOf — pure field access on an
// already-loaded *deploykit.BundleConfig.
func sidecarTemplatesOf(dc *deploykit.BundleConfig) map[string]json.RawMessage {
	if dc == nil {
		return nil
	}
	return dc.Sidecar
}

// secretDepNames mirrors charly/config_secret_migration.go's secretDepNames.
func secretDepNames(meta *spec.BoxMetadata) []string {
	if meta == nil || (len(meta.SecretRequire) == 0 && len(meta.SecretAccept) == 0) {
		return nil
	}
	names := make([]string, 0, len(meta.SecretRequire)+len(meta.SecretAccept))
	for _, dep := range meta.SecretRequire {
		names = append(names, dep.Name)
	}
	for _, dep := range meta.SecretAccept {
		names = append(names, dep.Name)
	}
	return names
}

// secretResolution mirrors charly-core's SecretResolution (secrets.go) field-for-field (same
// default Go json tags — Name/Source/Resolved/Required) so #PodConfigProvisionSecretsReply's
// ResolutionsJSON round-trips without a shared CUE type (a small, stable, JSON-only boundary).
type secretResolution struct {
	Name     string
	Source   string
	Resolved bool
	Required bool
}

// checkMissingEnvRequires mirrors charly-core's function of the same name.
func checkMissingEnvRequires(boxName string, requires []spec.EnvDependency, resolvedEnv []string) error {
	resolved := make(map[string]bool, len(resolvedEnv))
	for _, e := range resolvedEnv {
		if k, _, ok := strings.Cut(e, "="); ok {
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

// checkMissingSecretRequires mirrors charly-core's function of the same name, over the
// plugin-local secretResolution (the wire-decoded form of core's SecretResolution).
func checkMissingSecretRequires(boxName string, requires []spec.EnvDependency, resolutions []secretResolution) error {
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
	fmt.Fprintf(os.Stderr, "\nStore them in the credential backend, or pass -e once to auto-import:\n\n")
	fmt.Fprintf(os.Stderr, "  charly config %s", boxName)
	for _, dep := range missing {
		fmt.Fprintf(os.Stderr, " -e %s=...", dep.Name)
	}
	fmt.Fprintf(os.Stderr, "\n\n")
	return fmt.Errorf("missing required credential-backed secret(s) for %s", boxName)
}

// warnMissingMCPRequires mirrors charly-core's function of the same name.
func warnMissingMCPRequires(boxName string, requires []spec.EnvDependency, mcpServers []spec.MCPProvideEntry) {
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

// wellKnownInitDefs mirrors charly-core service.go's table VERBATIM — the frozen legacy fallback
// for pre-init_def-label images (do NOT add new entries; new init systems declare in the
// embedded vocabulary and bake into the label instead).
var wellKnownInitDefs = map[string]*spec.ResolvedInit{
	"supervisord": {
		Entrypoint:     []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
		ManagementTool: "supervisorctl",
		ManagementCommands: map[string]string{
			"status":  "status",
			"start":   "start {{.Service}}",
			"stop":    "stop {{.Service}}",
			"restart": "restart {{.Service}}",
		},
	},
	"systemd": {
		Entrypoint:     nil,
		ManagementTool: "systemctl",
		ManagementCommands: map[string]string{
			"status":  "--user status {{.Service}}",
			"start":   "--user start {{.Service}}",
			"stop":    "--user stop {{.Service}}",
			"restart": "--user restart {{.Service}}",
		},
	},
}

// resolveEntrypointFromMeta mirrors charly-core start.go's function of the same name.
func resolveEntrypointFromMeta(meta *spec.BoxMetadata) []string {
	if meta.Init == "" {
		return []string{"sleep", "infinity"}
	}
	if meta.InitDef != nil {
		return meta.InitDef.Entrypoint
	}
	if def, ok := wellKnownInitDefs[meta.Init]; ok {
		return def.Entrypoint
	}
	return []string{"sleep", "infinity"}
}

// parseVolumeFlags mirrors the former BoxConfigSetupCmd.parseVolumeFlags.
func parseVolumeFlags(c *spec.PodConfigSetupRequest) []spec.DeployVolume {
	var configs []spec.DeployVolume
	seen := make(map[string]bool)
	for _, v := range c.VolumeFlag {
		parts := strings.SplitN(v, ":", 3)
		dv := spec.DeployVolume{Name: parts[0]}
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
		if !seen[dv.Name] {
			configs = append(configs, dv)
			seen[dv.Name] = true
		}
	}
	for _, b := range c.Bind {
		if seen[b] || seen[strings.SplitN(b, "=", 2)[0]] {
			continue
		}
		if before, after, ok := strings.Cut(b, "="); ok {
			configs = append(configs, spec.DeployVolume{Name: before, Type: "bind", Host: after})
			seen[before] = true
		} else {
			configs = append(configs, spec.DeployVolume{Name: b, Type: "bind"})
			seen[b] = true
		}
	}
	for _, e := range c.Encrypt {
		if !seen[e] {
			configs = append(configs, spec.DeployVolume{Name: e, Type: "encrypted"})
			seen[e] = true
		}
	}
	return configs
}

// parseVolumeEnv mirrors the former charly-core function of the same name — pure env-var read.
func parseVolumeEnv(boxName string) []spec.DeployVolume {
	envVarName := "CHARLY_VOLUMES_" + strings.ToUpper(strings.ReplaceAll(boxName, "-", "_"))
	envVal := os.Getenv(envVarName)
	if envVal == "" {
		return nil
	}
	var configs []spec.DeployVolume
	for entry := range strings.SplitSeq(envVal, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		dv := spec.DeployVolume{Name: parts[0]}
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

// persistResourceCaps mirrors the former BoxConfigSetupCmd.persistResourceCaps: mutates the
// (already plugin-loaded) dc in place and re-saves it via the save-bundle seam iff any resource
// flag was set.
func persistResourceCaps(ctx context.Context, ex *sdk.Executor, dc **deploykit.BundleConfig, c *spec.PodConfigSetupRequest) error {
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
		entry.Security = &spec.SecurityConfig{}
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
	return saveBundle(ctx, ex, *dc)
}

// directDeployMarker + its dir/path/write/remove/IsDirectDeploy mirror the former charly-core
// functions VERBATIM — plain ~/.config/charly/direct/<name>.json I/O, host-independent of
// placement (the plugin runs on the same host).
type directDeployMarker struct {
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	Instance      string `json:"instance,omitempty"`
	ImageRef      string `json:"image_ref"`
	CreatedUTC    string `json:"created_utc"`
}

func directDeployMarkerDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home: %w", err)
	}
	return filepath.Join(home, ".config", "charly", "direct"), nil
}

func directDeployMarkerPath(box, instance string) (string, error) {
	dir, err := directDeployMarkerDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, kit.ContainerNameInstance(box, instance)+".json"), nil
}

// IsDirectDeploy mirrors charly-core's function of the same name (moved with the pod_lifecycle
// files it's shared with — see pod_lifecycle_resolve.go step (ii)).
func IsDirectDeploy(box, instance string) bool {
	path, err := directDeployMarkerPath(box, instance)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

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

// directPodmanArgs mirrors the former charly-core function of the same name VERBATIM.
func directPodmanArgs(qcfg deploykit.QuadletConfig, bindMounts []deploykit.ResolvedBindMount) []string {
	name := kit.ContainerNameInstance(qcfg.BoxName, qcfg.Instance)
	args := []string{"run", "-d", "--name", name, "--hostname", name, "--restart=always"}
	if qcfg.Network != "" {
		args = append(args, "--network", qcfg.Network)
	} else {
		args = append(args, "--network", "charly")
	}
	for _, p := range qcfg.Ports {
		if qcfg.BindAddress != "" && qcfg.BindAddress != "0.0.0.0" {
			args = append(args, "-p", qcfg.BindAddress+":"+p)
		} else {
			args = append(args, "-p", p)
		}
	}
	for _, v := range qcfg.Volumes {
		args = append(args, "-v", v.VolumeName+":"+v.ContainerPath)
	}
	for _, bm := range bindMounts {
		args = append(args, "-v", bm.HostPath+":"+bm.ContPath)
	}
	for _, e := range qcfg.Env {
		args = append(args, "-e", e)
	}
	if qcfg.EnvFile != "" {
		args = append(args, "--env-file", qcfg.EnvFile)
	}
	args = append(args, deploykit.SecurityArgs(qcfg.Security)...)
	if len(bindMounts) > 0 && qcfg.UID > 0 {
		args = append(args, "--userns", fmt.Sprintf("keep-id:uid=%d,gid=%d", qcfg.UID, qcfg.GID))
	}
	args = append(args, qcfg.ImageRef)
	args = append(args, qcfg.Entrypoint...)
	return args
}

// runConfigDirect mirrors the former BoxConfigSetupCmd.runConfigDirect VERBATIM.
func runConfigDirect(qcfg deploykit.QuadletConfig, bindMounts []deploykit.ResolvedBindMount, sidecars []deploykit.ResolvedSidecar, tunnelCfg *spec.TunnelConfig) error {
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
	_ = exec.Command("podman", "stop", name).Run()
	_ = exec.Command("podman", "rm", "-f", name).Run()
	args := directPodmanArgs(qcfg, bindMounts)
	cmd := exec.Command("podman", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman run %s failed: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	cid := strings.TrimSpace(string(out))
	fmt.Fprintf(os.Stderr, "Started %s (direct mode, container=%s)\n", name, cid[:min(12, len(cid))])
	if err := writeDirectDeployMarker(directDeployMarker{
		ContainerName: name, Image: qcfg.BoxName, Instance: qcfg.Instance, ImageRef: qcfg.ImageRef,
		CreatedUTC: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write direct-mode marker: %v\n", err)
	}
	return nil
}

// hasEncryptedBindMounts mirrors charly-core enc.go's function of the same name.
func hasEncryptedBindMounts(mounts []deploykit.ResolvedBindMount) bool {
	for _, m := range mounts {
		if m.Encrypted {
			return true
		}
	}
	return false
}

// workspaceBindHost mirrors charly-core volumes.go's function of the same name.
func workspaceBindHost(bindMounts []deploykit.ResolvedBindMount) string {
	for _, bm := range bindMounts {
		if bm.Name == "workspace" {
			return bm.HostPath
		}
	}
	return ""
}

// tunnelConfigPath mirrors charly-core tunnel.go's function of the same name — pure
// ~/.config/charly/tunnels/<name>.yml path construction.
func tunnelConfigPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "charly", "tunnels", name+".yml")
}

// podConfigWriteSelf renders + writes the quadlet/pod/sidecar/tunnel files IN-PROCESS (the
// direction-flip runs INSIDE this same plugin now, so no OpConfigWrite round-trip is needed —
// renderAndWritePodConfig in config_write.go is the shared body OpConfigWrite also calls).
func podConfigWriteSelf(r spec.PodConfigWriteRequest) (spec.PodConfigWriteReply, error) {
	return renderAndWritePodConfig(r)
}

// provisionData mirrors the former BoxConfigSetupCmd.runConfig's data-provisioning block.
func provisionData(ctx context.Context, ex *sdk.Executor, rt *kit.ResolvedRuntime, c *spec.PodConfigSetupRequest, meta *spec.BoxMetadata, imageRef string, dc *deploykit.BundleConfig, bindMounts []deploykit.ResolvedBindMount, volumes []deploykit.VolumeMount, deployVolumes []spec.DeployVolume) error {
	dataMeta := meta
	dataRef := imageRef
	dataEngine := rt.RunEngine
	if dc != nil {
		if entry, ok := dc.Lookup(c.Box, c.Instance); ok && entry.Engine != "" {
			dataEngine = entry.Engine
		}
	}

	if c.DataFrom != "" {
		dataRef = c.DataFrom
		if !strings.Contains(dataRef, ":") {
			if resolved, err := kit.ResolveNewestLocalCalVer(dataEngine, dataRef); err == nil && resolved != "" {
				dataRef = resolved
			}
		}
		var ensureRep spec.PodConfigEnsureImageReply
		if err := hostBuild(ctx, ex, podConfigEnsureImageKind, spec.PodConfigEnsureImageRequest{ImageRef: dataRef, BuildEngine: rt.BuildEngine}, &ensureRep); err != nil {
			return fmt.Errorf("extracting metadata from data image %s: %w", dataRef, err)
		}
		var dm spec.BoxMetadata
		if err := json.Unmarshal(ensureRep.MetaJSON, &dm); err != nil {
			return fmt.Errorf("extracting metadata from data image %s: %w", dataRef, err)
		}
		dataMeta = &dm
	}

	if len(dataMeta.DataEntries) == 0 {
		return nil
	}
	mode := deploykit.DataProvisionInitial
	if c.ForceSeed {
		mode = deploykit.DataProvisionForce
	} else {
		allSeeded := true
		for _, dvc := range deployVolumes {
			if !dvc.DataSeeded {
				allSeeded = false
				break
			}
		}
		if allSeeded && len(deployVolumes) > 0 {
			fmt.Fprintln(os.Stderr, "Data already provisioned (use --force-seed to re-provision)")
			return nil
		}
	}

	fmt.Fprintln(os.Stderr, "Provisioning data into volumes...")
	seeded, err := deploykit.ProvisionData(dataEngine, dataRef, dataMeta, bindMounts, volumes, c.Box, c.Instance, mode)
	if err != nil {
		return fmt.Errorf("data provisioning: %w", err)
	}
	if seeded == 0 {
		return nil
	}
	if dc == nil {
		dc = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	}
	key := deploykit.DeployKey(c.Box, c.Instance)
	imgDeploy := dc.Bundle[key]
	for i := range imgDeploy.Volume {
		for _, entry := range dataMeta.DataEntries {
			if imgDeploy.Volume[i].Name == entry.Volume {
				imgDeploy.Volume[i].DataSeeded = true
				imgDeploy.Volume[i].DataSource = dataRef
			}
		}
	}
	dc.Bundle[key] = imgDeploy
	if err := saveBundle(ctx, ex, dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save data seeded state to charly.yml: %v\n", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Provisioned data for %d volume(s)\n", seeded)
	return nil
}

// updateAllDeployedQuadlets mirrors the former charly-core function of the same name (moved
// wholesale, invoked from the setup flow's --update-all leaf via the SAME seams).
func updateAllDeployedQuadlets(ctx context.Context, ex *sdk.Executor, rt *kit.ResolvedRuntime, skipBox string) error {
	var loadRep spec.PodConfigLoadBundleReply
	if err := hostBuild(ctx, ex, podConfigLoadBundleKind, spec.PodConfigLoadDeployRequest{}, &loadRep); err != nil || len(loadRep.ConfigJSON) == 0 {
		return nil
	}
	var dc deploykit.BundleConfig
	if err := json.Unmarshal(loadRep.ConfigJSON, &dc); err != nil {
		return nil
	}

	keys := make([]string, 0, len(dc.Bundle))
	for key := range dc.Bundle {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var updated []string
	for _, key := range keys {
		if key == skipBox {
			continue
		}
		boxName, instance := deploykit.ParseDeployKey(key)
		qdir, err := kit.QuadletDir()
		if err != nil {
			continue
		}
		qpath := filepath.Join(qdir, kit.QuadletFilenameInstance(boxName, instance))
		if _, err := os.Stat(qpath); os.IsNotExist(err) {
			continue
		}

		imageRef, _ := extractQuadletImageLine(qpath)
		if imageRef == "" {
			var rep spec.PodConfigResolveRefReply
			if err := hostBuild(ctx, ex, podConfigResolveRefKind, spec.PodConfigResolveRefRequest{Box: boxName}, &rep); err == nil {
				imageRef = rep.ImageRef
			}
		}
		var ensureRep spec.PodConfigEnsureImageReply
		if err := hostBuild(ctx, ex, podConfigEnsureImageKind, spec.PodConfigEnsureImageRequest{ImageRef: imageRef, BuildEngine: rt.BuildEngine}, &ensureRep); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not read metadata for %s, skipping quadlet update\n", key)
			continue
		}
		var meta spec.BoxMetadata
		if err := json.Unmarshal(ensureRep.MetaJSON, &meta); err != nil {
			continue
		}
		deploykit.MergeDeployOntoMetadata(&meta, &dc, boxName, instance)

		updateCtrName := kit.ContainerNameInstance(boxName, instance)
		updateAccepted := deploykit.AcceptedEnvSet(meta.EnvAccept, meta.EnvRequire)
		globalEnv := dc.GlobalEnvForImage(deploykit.DeployKey(boxName, instance), updateCtrName, updateAccepted)
		envVars, err := kit.ResolveEnvVars(globalEnv, meta.Env, "", "", "", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not resolve env for %s: %v\n", key, err)
			continue
		}
		envVars = kit.EnrichNoProxy(envVars, dc.DeployedContainerNames())
		resolvedNetwork, _ := kit.ResolveNetwork(meta.Network, rt.RunEngine)

		var detectRep spec.PodConfigDetectDevicesReply
		_ = hostBuild(ctx, ex, podConfigDetectDevicesKind, spec.PodConfigDetectDevicesRequest{}, &detectRep)
		var detected spec.DetectedDevices
		if len(detectRep.DetectedJSON) > 0 {
			_ = json.Unmarshal(detectRep.DetectedJSON, &detected)
		}

		var deployVolumes []spec.DeployVolume
		var deploySidecarsRaw map[string]json.RawMessage
		if overlay, ok := dc.Bundle[key]; ok {
			deployVolumes = overlay.Volume
			deploySidecarsRaw = overlay.Sidecar
		}
		volumes, bindMounts := deploykit.ResolveVolumeBacking(boxName, instance, meta.Volume, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

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

		security := meta.Security
		if !security.Privileged {
			security.Devices = deploykit.AppendUnique(security.Devices, detected.Devices...)
			if detected.AMDGPU {
				security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
			}
		}
		envVars = appendAutoDetectedEnv(envVars, detected)

		var provRep spec.PodConfigProvisionSecretsReply
		_ = hostBuild(ctx, ex, podConfigProvisionSecretsKind, spec.PodConfigProvisionSecretsRequest{
			MetaJSON: ensureRep.MetaJSON, Box: boxName, Instance: instance, RunEngine: rt.RunEngine, AutoGen: true,
		}, &provRep)
		var provisioned []deploykit.CollectedSecret
		if len(provRep.ProvisionedJSON) > 0 {
			_ = json.Unmarshal(provRep.ProvisionedJSON, &provisioned)
		}
		if len(meta.SecretRequire) > 0 {
			var resolutions []secretResolution
			if len(provRep.ResolutionsJSON) > 0 {
				_ = json.Unmarshal(provRep.ResolutionsJSON, &resolutions)
			}
			missing := 0
			for _, r := range resolutions {
				if r.Required && !r.Resolved {
					missing++
				}
			}
			if missing > 0 {
				fmt.Fprintf(os.Stderr, "Warning: %s has %d unresolved secret_requires entries (quadlet regenerated; image may crashloop on restart)\n", key, missing)
			}
		}

		charlyBin, _ := os.Executable()
		isKeyring := provRep.IsKeyring

		var tunnelCfg *spec.TunnelConfig
		if meta.Tunnel != nil {
			var tRep spec.PodConfigTunnelResolveReply
			if err := hostBuild(ctx, ex, podConfigTunnelResolveKind, spec.PodConfigTunnelResolveRequest{MetaJSON: ensureRep.MetaJSON}, &tRep); err == nil && len(tRep.TunnelJSON) > 0 {
				var tc spec.TunnelConfig
				if json.Unmarshal(tRep.TunnelJSON, &tc) == nil {
					tunnelCfg = &tc
				}
			}
		}

		var resolvedSidecars []deploykit.ResolvedSidecar
		podName := ""
		if len(deploySidecarsRaw) > 0 {
			dsJSON, _ := json.Marshal(deploySidecarsRaw)
			ptJSON, _ := json.Marshal(sidecarTemplatesOf(&dc))
			var sidecarRep spec.PodConfigResolveSidecarsReply
			if err := hostBuild(ctx, ex, podConfigResolveSidecarsKind, spec.PodConfigResolveSidecarsRequest{
				DeploySidecarsJSON: dsJSON, ProjectTemplatesJSON: ptJSON, Box: boxName, Instance: instance,
				RunEngine: rt.RunEngine, AutoGen: true,
			}, &sidecarRep); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: resolving sidecars for %s: %v\n", key, err)
				continue
			}
			if len(sidecarRep.ResolvedSidecarsJSON) > 0 {
				_ = json.Unmarshal(sidecarRep.ResolvedSidecarsJSON, &resolvedSidecars)
			}
			if len(resolvedSidecars) > 0 {
				podName = kit.PodNameInstance(boxName, instance)
			}
		}

		qcfg := deploykit.QuadletConfig{
			BoxName: boxName, Instance: instance, ImageRef: imageRef, Home: meta.Home, Ports: meta.Port,
			Volumes: volumes, BindMounts: bindMounts, GPU: detected.GPU, BindAddress: rt.BindAddress,
			Tunnel: tunnelCfg, UID: meta.UID, GID: meta.GID, Env: envVars, EnvFile: quadletEnvFile,
			Security: security, Network: resolvedNetwork, Status: meta.Status, Info: meta.Info,
			Entrypoint: resolveEntrypointFromMeta(&meta), Secrets: provisioned, CharlyBin: charlyBin,
			EncryptedMounts: hasEncryptedBindMounts(bindMounts), KeyringBackend: isKeyring,
			PodName: podName, Sidecar: resolvedSidecars,
		}
		if quadletEnvFile != "" {
			qcfg.Env = append([]string{}, globalEnv...)
			qcfg.Env = appendAutoDetectedEnv(qcfg.Env, detected)
		}

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
		if _, err := podConfigWriteSelf(writeReq); err != nil {
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

// extractQuadletImageLine mirrors charly-core update_deploy_dispatch.go's function of the same
// name — pure regex read of an on-disk quadlet file.
func extractQuadletImageLine(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Image=") {
			return strings.TrimPrefix(line, "Image="), nil
		}
	}
	return "", nil
}

// runHook mirrors charly-core hooks.go's RunHook — pure exec.Command wrapper.
func runHook(engine, containerName, hookScript string, envVars []string) error {
	if hookScript == "" {
		return nil
	}
	args := []string{"exec"}
	args = append(args, "-e", "CHARLY_CONTAINER_NAME="+containerName)
	for _, env := range envVars {
		args = append(args, "-e", env)
	}
	args = append(args, containerName, "sh", "-c", hookScript)
	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "Running hook in %s...\n", containerName)
	return cmd.Run()
}

// prepareQuadletEnv mirrors the former BoxConfigSetupCmd.prepareQuadletEnv.
func prepareQuadletEnv(c *spec.PodConfigSetupRequest, dc *deploykit.BundleConfig, bindMounts []deploykit.ResolvedBindMount) string {
	var quadletEnvFile string
	if c.EnvFile != "" {
		if abs, err := filepath.Abs(c.EnvFile); err == nil {
			quadletEnvFile = abs
		}
	}
	if quadletEnvFile == "" && dc != nil {
		if overlay, ok := dc.Bundle[deploykit.DeployKey(c.Box, c.Instance)]; ok && overlay.EnvFile != "" {
			quadletEnvFile = kit.ExpandHostHome(overlay.EnvFile)
		}
	}
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

// removeDirectDeployMarker mirrors charly-core's function of the same name.
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

// resolveBoxName mirrors charly-core commands.go's function of the same name.
func resolveBoxName(box string) string {
	ref := kit.StripURLScheme(box)
	if spec.IsRemoteImageRef(ref) {
		return spec.ParseRemoteRef(ref).Name
	}
	return box
}

// findPodSidecarQuadlets mirrors charly-core sidecar.go's function of the same name — pure
// on-disk quadlet-dir scan (Pod=<podName>.pod directive match).
func findPodSidecarQuadlets(qdir, podName, mainContainerFile string) ([]string, error) {
	expected := fmt.Sprintf("Pod=%s.pod", podName)
	entries, err := os.ReadDir(qdir)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".container") || name == mainContainerFile {
			continue
		}
		content, rErr := os.ReadFile(filepath.Join(qdir, name))
		if rErr != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			if strings.TrimSpace(line) == expected {
				matches = append(matches, name)
				break
			}
		}
	}
	return matches, nil
}

// appendGroupsForAMDGPU / appendAutoDetectedEnv / appendEnvUnique mirror charly-core devices.go's
// functions of the same name.
func appendGroupsForAMDGPU(groups []string) []string {
	for _, g := range groups {
		if g == "keep-groups" {
			return groups
		}
	}
	return append(groups, "keep-groups")
}

func appendAutoDetectedEnv(envVars []string, detected spec.DetectedDevices) []string {
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}
	if detected.RenderNode != "" {
		envVars = appendEnvUnique(envVars, "DRINODE="+detected.RenderNode)
		envVars = appendEnvUnique(envVars, "DRI_NODE="+detected.RenderNode)
	}
	return envVars
}

func appendEnvUnique(envVars []string, kv string) []string {
	key := strings.SplitN(kv, "=", 2)[0] + "="
	for _, e := range envVars {
		if strings.HasPrefix(e, key) {
			return envVars
		}
	}
	return append(envVars, kv)
}
