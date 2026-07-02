package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// Sidecars reconstructs the name-keyed sidecar-template library from uf.PluginKinds.
// The `sidecar` kind is a plugin kind (candy/plugin-sidecar) — a `sidecar:` node (incl.
// the binary-embedded `tailscale` template) lands in uf.PluginKinds["sidecar"][<name>]
// as canonical spec.Sidecar JSON (produced by the plugin's Invoke). This accessor
// decodes each body back into a SidecarDef (= spec.Sidecar) value, yielding the SAME
// map[string]SidecarDef shape the deploy/quadlet code consumed when sidecar was a typed
// core map (the former uf.Sidecar). It is the single point where the projections
// (Config.Sidecar / BundleConfig.Sidecar) and EmbeddedSidecarTemplates read the
// templates. Recomputed per call (the library is a handful of templates); returns nil
// when none are configured. A decode error is impossible in practice — the body is
// canonical JSON the plugin Marshalled from spec.Sidecar — but a bad entry is skipped
// rather than poisoning the whole library.
func (uf *UnifiedFile) Sidecars() map[string]SidecarDef {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["sidecar"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]SidecarDef, len(bodies))
	for name, body := range bodies {
		var s SidecarDef
		if err := json.Unmarshal(body, &s); err != nil {
			continue
		}
		out[name] = s
	}
	return out
}

// SidecarKeyContext is the template input for SidecarSecret.EnvFrom rendering.
// Exposes the resolved Parameter map as `.Parameter` (so templates can
// reference {{.Parameter.tailnet}} etc.).
type SidecarKeyContext struct {
	Parameter map[string]string
}

// sidecarTemplateFuncs are the text/template funcs available inside
// SidecarSecret.EnvFrom expressions. Centralized so both `renderSidecarEnvFrom`
// and tests use the same set.
var sidecarTemplateFuncs = template.FuncMap{
	// tailnetEnvSuffix normalizes a MagicDNS suffix like "armadillo-quail.ts.net"
	// into a valid env-var-name fragment "ARMADILLO_QUAIL_TS_NET" by uppercasing
	// and replacing every non-alphanumeric character with '_'. Empty input → "".
	"tailnetEnvSuffix": func(s string) string {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z':
				b.WriteRune(r - 32) // to upper
			case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			default:
				b.WriteRune('_')
			}
		}
		return b.String()
	},
}

// renderSidecarEnvFrom resolves the SidecarSecret.EnvFrom text/template against
// the SidecarDef's Parameter map. Returns the resolved HOST-side env var name,
// OR an error naming the missing required parameter.
//
// When SidecarSecret.EnvFrom is empty, falls back to the SidecarSecret.Env
// value (legacy single-tailnet behavior: host env var name == container env
// var name).
func renderSidecarEnvFrom(s SidecarSecret, params map[string]string) (string, error) {
	if s.EnvFrom == "" {
		return s.Env, nil
	}
	// Pre-check: every {{.Parameter.X}} reference must resolve to a
	// non-empty value. text/template's `missingkey=zero` only catches
	// direct map misses, not values that pipe through a func to empty
	// (e.g. `{{.Parameter.tailnet | tailnetEnvSuffix}}` with tailnet=""
	// happily renders to "" — silently masking the missing parameter).
	// Doing the check up-front gives a precise remediation hint.
	for paramName := range extractParameterRefs(s.EnvFrom) {
		v, ok := params[paramName]
		if !ok || v == "" {
			return "", fmt.Errorf("sidecar secret %q references parameter %q which is unset. "+
				"Set `sidecars.<sidecar-name>.parameter.%s: <value>` in charly.yml",
				s.Name, paramName, paramName)
		}
	}
	tmpl, err := template.New("sidecar-env-from").Funcs(sidecarTemplateFuncs).Parse(s.EnvFrom)
	if err != nil {
		return "", fmt.Errorf("sidecar secret %q: parsing env_from template: %w", s.Name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, SidecarKeyContext{Parameter: params}); err != nil {
		return "", fmt.Errorf("sidecar secret %q: rendering env_from template: %w", s.Name, err)
	}
	return buf.String(), nil
}

// extractParameterRefs scans the text/template string for {{.Parameter.<name>}}
// references and returns the set of parameter names. Used by renderSidecarEnvFrom
// to produce a useful error when a referenced parameter is unset.
func extractParameterRefs(tmplStr string) map[string]struct{} {
	out := map[string]struct{}{}
	const marker = "{{.Parameter."
	idx := 0
	for {
		i := strings.Index(tmplStr[idx:], marker)
		if i < 0 {
			break
		}
		start := idx + i + len(marker)
		end := strings.IndexAny(tmplStr[start:], "}.| ")
		if end < 0 {
			break
		}
		name := tmplStr[start : start+end]
		if name != "" {
			out[name] = struct{}{}
		}
		idx = start + end
	}
	return out
}

// ResolvedSidecar is a fully resolved sidecar ready for quadlet generation.
type ResolvedSidecar struct {
	Name     string            // sidecar key (e.g., "tailscale")
	Image    string            // resolved OCI image ref
	Env      map[string]string // merged env vars (sorted for deterministic output)
	Secret   []CollectedSecret // provisioned podman secrets
	Volume   []VolumeMount     // resolved named volumes
	Security SecurityConfig    // merged security config
}

// MergeSidecar merges sidecar definitions from base into overlay.
// For each sidecar name:
//   - image: overlay replaces if non-empty
//   - env: map merge (overlay keys win, base keys preserved)
//   - secrets: overlay replaces entirely
//   - volumes: overlay replaces entirely
//   - security: overlay replaces entirely
//   - description: overlay replaces if non-empty
//
// Sidecars in base but not overlay are inherited.
// Sidecars in overlay but not base are added.
func MergeSidecar(base, overlay map[string]SidecarDef) map[string]SidecarDef {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	if len(base) == 0 {
		return overlay
	}
	if len(overlay) == 0 {
		result := make(map[string]SidecarDef, len(base))
		maps.Copy(result, base)
		return result
	}

	result := make(map[string]SidecarDef, len(base)+len(overlay))

	for name, baseDef := range base {
		if overlayDef, ok := overlay[name]; ok {
			result[name] = mergeSingleSidecar(baseDef, overlayDef)
		} else {
			result[name] = baseDef
		}
	}

	for name, overlayDef := range overlay {
		if _, ok := base[name]; !ok {
			result[name] = overlayDef
		}
	}

	return result
}

func mergeSingleSidecar(base, overlay SidecarDef) SidecarDef {
	merged := base

	if overlay.Description != "" {
		merged.Description = overlay.Description
	}
	if overlay.Image != "" {
		merged.Image = overlay.Image
	}
	if overlay.Secret != nil {
		merged.Secret = overlay.Secret
	}
	if overlay.Volume != nil {
		merged.Volume = overlay.Volume
	}
	if overlay.Security != nil {
		merged.Security = overlay.Security
	}

	// Parameter — map merge: deploy keys win, template defaults preserved.
	// The template uses empty-string sentinels ("") for required parameters;
	// after merge, an empty value means the deploy didn't supply it, and
	// renderSidecarEnvFrom raises a clear error at charly-config time.
	if len(base.Parameter) > 0 || len(overlay.Parameter) > 0 {
		mergedParam := make(map[string]string, len(base.Parameter)+len(overlay.Parameter))
		maps.Copy(mergedParam, base.Parameter)
		maps.Copy(mergedParam, overlay.Parameter)
		merged.Parameter = mergedParam
	}

	if len(overlay.Env) > 0 {
		mergedEnv := make(map[string]string, len(base.Env)+len(overlay.Env))
		maps.Copy(mergedEnv, base.Env)
		maps.Copy(mergedEnv, overlay.Env)
		merged.Env = mergedEnv
	}

	return merged
}

// ResolveSidecar resolves sidecar definitions into generation-ready configs.
// Resolves volume names (charly-<image>-<sidecar>-<vol>), collects secrets,
// and renders each SidecarSecret.EnvFrom template against the merged
// Parameter map. Missing required parameters surface as fmt.Errorf
// returned via the error channel; callers must check it before proceeding.
func ResolveSidecar(defs map[string]SidecarDef, boxName, instance string) ([]ResolvedSidecar, error) {
	if len(defs) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	var resolved []ResolvedSidecar
	for _, name := range names {
		def := defs[name]
		sc := ResolvedSidecar{
			Name:  name,
			Image: def.Image,
			Env:   def.Env,
		}

		if def.Security != nil {
			sc.Security = *def.Security
		}

		for _, v := range def.Volume {
			volName := sidecarVolumeName(boxName, name, v.Name)
			if instance != "" {
				volName = sidecarVolumeName(boxName+"-"+instance, name, v.Name)
			}
			sc.Volume = append(sc.Volume, VolumeMount{
				VolumeName:    volName,
				ContainerPath: v.Path,
			})
		}

		for _, s := range def.Secret {
			secretName := sidecarSecretName(boxName, name, s.Name)
			if instance != "" {
				secretName = sidecarSecretName(boxName+"-"+instance, name, s.Name)
			}
			hostEnv, err := renderSidecarEnvFrom(s, def.Parameter)
			if err != nil {
				return nil, fmt.Errorf("sidecar %q: %w", name, err)
			}
			sc.Secret = append(sc.Secret, CollectedSecret{
				Name:       secretName,
				Env:        s.Env,
				HostEnv:    hostEnv,
				SecretName: s.Name,
			})
		}

		resolved = append(resolved, sc)
	}

	return resolved, nil
}

// sidecarTemplatesOf returns the project-root sidecar templates carried by a
// deploy config (nil-safe). These extend/override the embedded template set in
// ResolveSidecarsForConfig.
func sidecarTemplatesOf(dc *BundleConfig) map[string]SidecarDef {
	if dc == nil {
		return nil
	}
	return dc.Sidecar
}

// EmbeddedSidecarTemplates returns the binary-embedded sidecar-template
// library (the charly.yml `sidecar:` section), read through the unified loader
// (embeddedDefaults). It is the deploy-time template base for any sidecar a
// deploy attaches.
func EmbeddedSidecarTemplates() (map[string]SidecarDef, error) {
	def, err := embeddedDefaults()
	if err != nil {
		return nil, err
	}
	// sidecar is a plugin kind now: the embedded `tailscale` template lands in
	// def.PluginKinds["sidecar"], read back via the Sidecars() accessor.
	return def.Sidecars(), nil
}

// ResolveSidecarsForConfig builds the effective sidecar defs for a deploy: the
// embedded template library is the BASE, a project's own root `sidecar:`
// templates (projectTemplates) extend/override it, and each deploy's per-node
// `sidecar:` overrides (deploySidecars) win last. Only sidecars referenced in
// deploySidecars are returned.
func ResolveSidecarsForConfig(projectTemplates, deploySidecars map[string]SidecarDef) (map[string]SidecarDef, error) {
	if len(deploySidecars) == 0 {
		return nil, nil
	}

	base, err := EmbeddedSidecarTemplates()
	if err != nil {
		return nil, err
	}
	// Project-declared root sidecar: templates extend/override the embedded set.
	if len(projectTemplates) > 0 {
		base = MergeSidecar(base, projectTemplates)
	}

	// Per-deploy overrides win last.
	merged := MergeSidecar(base, deploySidecars)

	// Filter: only keep sidecars that the deploy actually references.
	filtered := make(map[string]SidecarDef, len(deploySidecars))
	for name := range deploySidecars {
		if def, ok := merged[name]; ok {
			filtered[name] = def
		}
	}

	if len(filtered) == 0 {
		return nil, nil
	}
	return filtered, nil
}

// SidecarEnvKey returns all env var keys defined by attached sidecars.
// Used to route CLI -e flags to sidecars vs the app container.
func SidecarEnvKey(sidecars map[string]SidecarDef) map[string]string {
	keys := make(map[string]string) // env key -> sidecar name
	for scName, sc := range sidecars {
		for k := range sc.Env {
			keys[k] = scName
		}
		for _, s := range sc.Secret {
			if s.Env != "" {
				keys[s.Env] = scName
			}
		}
	}
	// Also include well-known TS_ prefix for tailscale sidecar
	if _, ok := sidecars["tailscale"]; ok {
		for _, k := range []string{"TS_HOSTNAME", "TS_EXTRA_ARGS", "TS_TAILSCALED_EXTRA_ARGS", "TS_DEBUG_FIREWALL_MODE", "TS_ROUTES", "TS_SERVE_CONFIG", "TS_LOGIN_SERVER"} {
			keys[k] = "tailscale"
		}
	}
	return keys
}

// --- Naming helpers ---

func sidecarVolumeName(boxName, sidecarName, volumeName string) string {
	return fmt.Sprintf("charly-%s-%s-%s", boxName, sidecarName, volumeName)
}

func sidecarSecretName(boxName, sidecarName, secretName string) string {
	return fmt.Sprintf("charly-%s-%s-%s", boxName, sidecarName, secretName)
}

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

func HasTailscaleSidecar(sidecars map[string]SidecarDef) bool {
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

func SortedSidecarEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+env[k])
	}
	return result
}

func IsSidecarEnvQuotable(val string) bool {
	return strings.ContainsAny(val, `"{}[] `)
}
