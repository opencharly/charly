package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// sidecar.go — the HOST side of the `sidecar` kind after the sidecar de-type
// (Cutover D). ALL sidecar BUSINESS LOGIC — the embedded+project+deploy template
// merge, CLI env-flag routing, and volume/secret-name + env_from resolution — lives
// in candy/plugin-sidecar's OpResolve leg. The host holds only OPAQUE sidecar bodies
// (map[string]json.RawMessage) and consumes the resolved ResolvedSidecar values this
// file's adapter builds; the quadlet/naming helpers below are pure host machinery.

// ResolvedSidecar (the host-adapted, generation-ready sidecar form the sidecar plugin's
// spec.ResolvedSidecar wire type is adapted into) is a deploykit resolved-runtime type
// now, aliased in deploykit_pod_aliases.go — it moved to sdk/deploykit with the pod
// config-write mechanism (P11), since its CollectedSecret/VolumeMount/SecurityConfig
// fields are all deploykit/vmshared types.

// resolveSidecarsViaPlugin invokes candy/plugin-sidecar's OpResolve leg — the single
// point where sidecar defs are resolved. The host passes OPAQUE def layers + the CLI
// env; the plugin returns generation-ready sidecars, the app-only env, and the routed
// deploy overrides to persist. The kernel reads no spec.Sidecar fields.
func resolveSidecarsViaPlugin(in spec.SidecarResolveInput) (spec.SidecarResolveReply, error) {
	reply, err := hostInvoke[spec.SidecarResolveInput, spec.SidecarResolveReply](ClassKind, "sidecar", OpResolve, in)
	if err != nil {
		return spec.SidecarResolveReply{}, fmt.Errorf("sidecar resolve: %w", err)
	}
	return reply, nil
}

// resolvedSidecarFromSpec adapts one plugin-resolved spec.ResolvedSidecar into the
// host's ResolvedSidecar (the quadlet-gen shape).
func resolvedSidecarFromSpec(s spec.ResolvedSidecar) ResolvedSidecar {
	rs := ResolvedSidecar{Name: s.Name, Image: s.Image, Env: s.Env}
	if s.Security != nil {
		rs.Security = *s.Security
	}
	for _, v := range s.Volume {
		rs.Volume = append(rs.Volume, VolumeMount(v))
	}
	for _, sec := range s.Secret {
		rs.Secret = append(rs.Secret, CollectedSecret{
			Name:       sec.Name,
			Env:        sec.Env,
			HostEnv:    sec.HostEnv,
			SecretName: sec.SecretName,
		})
	}
	return rs
}

// embeddedSidecarBodies returns the binary-embedded sidecar-template library (the
// charly.yml `sidecar:` section) as OPAQUE bodies, read through the unified loader.
// It is the deploy-time template base the sidecar plugin merges under a deploy's
// own overrides.
func embeddedSidecarBodies() (map[string]json.RawMessage, error) {
	def, err := embeddedDefaults()
	if err != nil {
		return nil, err
	}
	return def.PluginKinds["sidecar"], nil
}

// sidecarTemplatesOf returns the project-root sidecar templates carried by a deploy
// config (nil-safe), as OPAQUE bodies. These extend/override the embedded set inside
// the sidecar plugin's OpResolve.
func sidecarTemplatesOf(dc *BundleConfig) map[string]json.RawMessage {
	if dc == nil {
		return nil
	}
	return dc.Sidecar
}

// --- Naming helpers ---

func SidecarContainerName(boxName, sidecarName string) string {
	return containerName(boxName) + "-" + sidecarName
}

func SidecarContainerNameInstance(boxName, instance, sidecarName string) string {
	return containerNameInstance(boxName, instance) + "-" + sidecarName
}

func PodName(boxName string) string {
	return containerName(boxName)
}

func PodNameInstance(boxName, instance string) string {
	return containerNameInstance(boxName, instance)
}

// findPodSidecarQuadlets returns the .container quadlets in qdir that belong
// to the pod podName, identified by the load-bearing `Pod=<podName>.pod`
// directive inside the quadlet's [Container] section. Filename-prefix
// matching is NOT used because it collides with sibling instances of the
// same image (e.g. charly-versa-ecovoyage.container is an instance of versa,
// NOT a sidecar of pod charly-versa.pod). Only true pod members carry the
// Pod= directive — sibling instances and standalone container deploys
// do not. mainContainerFile (typically the main pod container's quadlet
// filename) is excluded from the returned list because its lifecycle is
// owned by the caller's main systemctl disable, not by the sidecar sweep.
func findPodSidecarQuadlets(qdir, podName, mainContainerFile string) ([]string, error) {
	expected := fmt.Sprintf("Pod=%s.pod", podName)
	entries, err := os.ReadDir(qdir)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".container") {
			continue
		}
		if name == mainContainerFile {
			continue
		}
		content, rErr := os.ReadFile(filepath.Join(qdir, name))
		if rErr != nil {
			continue
		}
		for line := range strings.SplitSeq(string(content), "\n") {
			if strings.TrimSpace(line) == expected {
				matches = append(matches, name)
				break
			}
		}
	}
	sort.Strings(matches)
	return matches, nil
}

// HasTailscaleSidecar reports whether a name-keyed sidecar map (opaque bodies)
// attaches the tailscale sidecar — a pure key-existence check.
func HasTailscaleSidecar(sidecars map[string]json.RawMessage) bool {
	_, ok := sidecars["tailscale"]
	return ok
}

func sidecarConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "sidecar"), nil
}
