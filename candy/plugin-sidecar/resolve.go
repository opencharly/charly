package sidecarkind

// resolve.go — candy/plugin-sidecar's OpResolve leg (the host-side sidecar de-type,
// Cutover D). ALL sidecar business logic the kernel used to do inline lives HERE
// now: CLI env-flag routing, the embedded+project+deploy template MERGE, and the
// volume/secret-name + env_from RESOLUTION. The host holds only OPAQUE sidecar
// bodies (json.RawMessage) and consumes the generation-ready spec.ResolvedSidecar
// values this returns — it reads zero spec.Sidecar fields.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"text/template"

	"github.com/opencharly/sdk/spec"
)

// resolveSidecars is the OpResolve entry point. It routes CLI env, merges the three
// def layers (embedded base < project templates < per-deploy overrides), filters to
// the deploy's referenced sidecars, resolves each into a spec.ResolvedSidecar, and
// returns the app-only env + the routed deploy overrides (opaque, for the host to
// persist to charly.yml).
func resolveSidecars(in spec.SidecarResolveInput) (spec.SidecarResolveReply, error) {
	embedded, err := decodeSidecarMap(in.EmbeddedTemplates)
	if err != nil {
		return spec.SidecarResolveReply{}, fmt.Errorf("embedded templates: %w", err)
	}
	project, err := decodeSidecarMap(in.ProjectTemplates)
	if err != nil {
		return spec.SidecarResolveReply{}, fmt.Errorf("project templates: %w", err)
	}
	deploy, err := decodeSidecarMap(in.DeployOverrides)
	if err != nil {
		return spec.SidecarResolveReply{}, fmt.Errorf("deploy overrides: %w", err)
	}

	// Route CLI -e flags: a sidecar-declared env key goes to that sidecar, else the app.
	envKeys := sidecarEnvKey(deploy)
	var appEnv []string
	for _, e := range in.CliEnv {
		key := e
		if before, _, ok := strings.Cut(e, "="); ok {
			key = before
		}
		if scName, ok := envKeys[key]; ok {
			sc := deploy[scName]
			if sc.Env == nil {
				sc.Env = spec.StrMap{}
			}
			if _, after, ok := strings.Cut(e, "="); ok {
				sc.Env[key] = after
			}
			deploy[scName] = sc
		} else {
			appEnv = append(appEnv, e)
		}
	}

	// Merge (embedded < project < deploy) + filter to deploy refs.
	merged := mergeForConfig(embedded, project, deploy)

	// Resolve each merged def → generation-ready ResolvedSidecar.
	resolved, err := resolveEach(merged, in.Box, in.Instance)
	if err != nil {
		return spec.SidecarResolveReply{}, err
	}

	// Return the routed deploy overrides as OPAQUE bodies for the host to persist.
	persist := make(map[string]json.RawMessage, len(deploy))
	for name, sc := range deploy {
		b, mErr := json.Marshal(sc)
		if mErr != nil {
			return spec.SidecarResolveReply{}, fmt.Errorf("sidecar %q: marshal persist body: %w", name, mErr)
		}
		persist[name] = b
	}

	return spec.SidecarResolveReply{Sidecars: resolved, AppEnv: appEnv, PersistOverrides: persist}, nil
}

// decodeSidecarMap decodes a name→opaque-body map into name→spec.Sidecar.
func decodeSidecarMap(bodies map[string]json.RawMessage) (map[string]spec.Sidecar, error) {
	if len(bodies) == 0 {
		return nil, nil
	}
	out := make(map[string]spec.Sidecar, len(bodies))
	for name, body := range bodies {
		var s spec.Sidecar
		if err := json.Unmarshal(body, &s); err != nil {
			return nil, fmt.Errorf("sidecar %q: %w", name, err)
		}
		out[name] = s
	}
	return out, nil
}

// mergeForConfig builds the effective sidecar defs: embedded base < project
// templates < per-deploy overrides, filtered to only the sidecars the deploy
// references.
func mergeForConfig(embedded, project, deploy map[string]spec.Sidecar) map[string]spec.Sidecar {
	if len(deploy) == 0 {
		return nil
	}
	base := embedded
	if len(project) > 0 {
		base = mergeSidecar(base, project)
	}
	merged := mergeSidecar(base, deploy)
	filtered := make(map[string]spec.Sidecar, len(deploy))
	for name := range deploy {
		if def, ok := merged[name]; ok {
			filtered[name] = def
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// mergeSidecar merges overlay into base (overlay wins). Base-only entries inherit;
// overlay-only entries are added.
func mergeSidecar(base, overlay map[string]spec.Sidecar) map[string]spec.Sidecar {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	if len(base) == 0 {
		return overlay
	}
	if len(overlay) == 0 {
		result := make(map[string]spec.Sidecar, len(base))
		maps.Copy(result, base)
		return result
	}
	result := make(map[string]spec.Sidecar, len(base)+len(overlay))
	for name, baseDef := range base {
		if overlayDef, ok := overlay[name]; ok {
			result[name] = mergeSingle(baseDef, overlayDef)
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

func mergeSingle(base, overlay spec.Sidecar) spec.Sidecar {
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
	// Parameter — map merge: deploy keys win, template "" sentinels preserved.
	if len(base.Parameter) > 0 || len(overlay.Parameter) > 0 {
		mergedParam := make(spec.StrMap, len(base.Parameter)+len(overlay.Parameter))
		maps.Copy(mergedParam, base.Parameter)
		maps.Copy(mergedParam, overlay.Parameter)
		merged.Parameter = mergedParam
	}
	if len(overlay.Env) > 0 {
		mergedEnv := make(spec.StrMap, len(base.Env)+len(overlay.Env))
		maps.Copy(mergedEnv, base.Env)
		maps.Copy(mergedEnv, overlay.Env)
		merged.Env = mergedEnv
	}
	return merged
}

// resolveEach resolves merged sidecar defs into generation-ready ResolvedSidecars:
// scoped volume/secret names + rendered env_from.
func resolveEach(defs map[string]spec.Sidecar, box, instance string) ([]spec.ResolvedSidecar, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	scope := box
	if instance != "" {
		scope = box + "-" + instance
	}

	var resolved []spec.ResolvedSidecar
	for _, name := range names {
		def := defs[name]
		sc := spec.ResolvedSidecar{Name: name, Image: def.Image, Env: def.Env, Security: def.Security}

		for _, v := range def.Volume {
			sc.Volume = append(sc.Volume, spec.ResolvedSidecarVolume{
				VolumeName:    fmt.Sprintf("charly-%s-%s-%s", scope, name, v.Name),
				ContainerPath: v.Path,
			})
		}
		for _, s := range def.Secret {
			hostEnv, err := renderEnvFrom(s, def.Parameter)
			if err != nil {
				return nil, fmt.Errorf("sidecar %q: %w", name, err)
			}
			sc.Secret = append(sc.Secret, spec.ResolvedSidecarSecret{
				Name:       fmt.Sprintf("charly-%s-%s-%s", scope, name, s.Name),
				Env:        s.Env,
				HostEnv:    hostEnv,
				SecretName: s.Name,
			})
		}
		resolved = append(resolved, sc)
	}
	return resolved, nil
}

// sidecarEnvKey returns every env var key defined by attached sidecars → sidecar
// name (for routing CLI -e flags). Includes the well-known TS_ tailscale prefix.
func sidecarEnvKey(sidecars map[string]spec.Sidecar) map[string]string {
	keys := make(map[string]string)
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
	if _, ok := sidecars["tailscale"]; ok {
		for _, k := range []string{"TS_HOSTNAME", "TS_EXTRA_ARGS", "TS_TAILSCALED_EXTRA_ARGS", "TS_DEBUG_FIREWALL_MODE", "TS_ROUTES", "TS_SERVE_CONFIG", "TS_LOGIN_SERVER"} {
			keys[k] = "tailscale"
		}
	}
	return keys
}

// sidecarTemplateFuncs are the funcs available inside SidecarSecret.EnvFrom.
var sidecarTemplateFuncs = template.FuncMap{
	"tailnetEnvSuffix": func(s string) string {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z':
				b.WriteRune(r - 32)
			case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			default:
				b.WriteRune('_')
			}
		}
		return b.String()
	},
}

// renderEnvFrom resolves the SidecarSecret.EnvFrom template against the merged
// Parameter map (host-side env var name). Empty EnvFrom → the plain Env name.
func renderEnvFrom(s spec.SidecarSecret, params spec.StrMap) (string, error) {
	if s.EnvFrom == "" {
		return s.Env, nil
	}
	for paramName := range extractParameterRefs(s.EnvFrom) {
		v, ok := params[paramName]
		if !ok || v == "" {
			return "", fmt.Errorf("sidecar secret %q references parameter %q which is unset. "+
				"Set `sidecar.<name>.parameter.%s: <value>` in charly.yml", s.Name, paramName, paramName)
		}
	}
	tmpl, err := template.New("sidecar-env-from").Funcs(sidecarTemplateFuncs).Parse(s.EnvFrom)
	if err != nil {
		return "", fmt.Errorf("sidecar secret %q: parsing env_from template: %w", s.Name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Parameter spec.StrMap }{Parameter: params}); err != nil {
		return "", fmt.Errorf("sidecar secret %q: rendering env_from template: %w", s.Name, err)
	}
	return buf.String(), nil
}

// extractParameterRefs scans a template for {{.Parameter.<name>}} references.
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
		if name := tmplStr[start : start+end]; name != "" {
			out[name] = struct{}{}
		}
		idx = start + end
	}
	return out
}
