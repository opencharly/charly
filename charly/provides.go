package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// EnvProvideEntry is a resolved env_provides entry in charly.yml.
type EnvProvideEntry struct {
	Name   string `yaml:"name" json:"name"`
	Value  string `yaml:"value" json:"value"`
	Source string `yaml:"source" json:"source"`
}

// MCPProvideEntry is a resolved mcp_provides entry in charly.yml.
type MCPProvideEntry struct {
	Name      string `yaml:"name" json:"name"`
	URL       string `yaml:"url" json:"url"`
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
	Source    string `yaml:"source" json:"source"`
}

// ProvidesConfig holds all resolved provides entries in charly.yml.
type ProvidesConfig struct {
	Env []EnvProvideEntry `yaml:"env,omitempty" json:"env,omitempty"`
	MCP []MCPProvideEntry `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}

// Named is the interface for provides entries (shared pipeline logic).
type Named interface {
	GetName() string
	GetSource() string
}

func (e EnvProvideEntry) GetName() string   { return e.Name }
func (e EnvProvideEntry) GetSource() string { return e.Source }
func (e MCPProvideEntry) GetName() string   { return e.Name }
func (e MCPProvideEntry) GetSource() string { return e.Source }

// filterOwnProvides removes entries injected by the given image (self-exclusion).
// NOTE: No longer used in GlobalEnvForImage (replaced by podAwareEnvProvides).
// Kept for removeBySource and other callers that need strict exclusion.
func filterOwnProvides[T Named](entries []T, boxName string) []T {
	if boxName == "" {
		return entries
	}
	var result []T
	for _, e := range entries {
		if e.GetSource() != boxName {
			result = append(result, e)
		}
	}
	return result
}

// podAwareEnvProvides resolves env entries for a specific consumer deploy.
// Same-deploy entries (source == consumerKey EXACTLY) get container hostname
// rewritten to localhost (pod: co-located services). Different-deploy entries
// keep their container hostname URLs. If both local and remote share a name,
// local wins.
//
// `consumerKey` is the charly.yml map key — base image name (e.g. "versa") or
// image-with-instance (e.g. "versa/ecovoyage"). Using prefix-match here is a
// bug: `isSameBaseBox("versa/ecovoyage", "versa")` returns true (deletion
// semantics), which would let another instance's env_provides leak into the
// base consumer's runtime env and trigger a second-order failure when
// strings.ReplaceAll("charly-versa-ecovoyage", "charly-versa", "localhost") produces
// the malformed hostname "localhost-ecovoyage". Exact match is correct.
func podAwareEnvProvides(entries []EnvProvideEntry, consumerKey, ctrName string) []EnvProvideEntry {
	var result []EnvProvideEntry
	seen := map[string]bool{} // name → true if local entry added
	// First pass: same-deploy entries with localhost rewrite
	for _, e := range entries {
		if e.Source == consumerKey {
			local := e
			local.Value = strings.ReplaceAll(e.Value, ctrName, "localhost")
			result = append(result, local)
			seen[e.Name] = true
		}
	}
	// Second pass: cross-deploy entries (skip if local exists with same name)
	for _, e := range entries {
		if e.Source != consumerKey && !seen[e.Name] {
			result = append(result, e)
		}
	}
	return result
}

// removeBySource removes all entries injected by the given image.
// Returns the filtered list and whether anything was removed.
func removeBySource[T Named](entries []T, boxName string) ([]T, bool) {
	var result []T
	removed := false
	for _, e := range entries {
		if isSameBaseBox(e.GetSource(), boxName) {
			removed = true
		} else {
			result = append(result, e)
		}
	}
	return result, removed
}

// removeByExactSource removes entries whose source matches the exact deploy key.
// Unlike removeBySource, this does not match other instances of the same base image.
func removeByExactSource[T Named](entries []T, source string) ([]T, bool) {
	var result []T
	removed := false
	for _, e := range entries {
		if e.GetSource() == source {
			removed = true
		} else {
			result = append(result, e)
		}
	}
	return result, removed
}

// podAwareMCPProvides resolves MCP entries for a specific consumer deploy.
// Same-deploy entries (source == consumerKey EXACTLY) get localhost URLs.
// Different-deploy entries keep their container hostname URLs. If both
// local and remote share a name, local wins. See podAwareEnvProvides for
// the rationale on exact-match vs prefix-match.
func podAwareMCPProvides(entries []MCPProvideEntry, consumerKey, ctrName string) []MCPProvideEntry {
	var result []MCPProvideEntry
	seen := map[string]bool{} // name → true if local entry added
	// First pass: same-deploy entries with localhost rewrite
	for _, e := range entries {
		if e.Source == consumerKey {
			local := e
			local.URL = strings.ReplaceAll(e.URL, ctrName, "localhost")
			result = append(result, local)
			seen[e.Name] = true
		}
	}
	// Second pass: cross-deploy entries (skip if local exists with same name)
	for _, e := range entries {
		if e.Source != consumerKey && !seen[e.Name] {
			result = append(result, e)
		}
	}
	return result
}

// GlobalEnvForImage builds env vars for a consumer from global provides.
// Returns flat env var slice ready for ResolveEnvVars.
//
// acceptedEnv controls which env_provides vars are injected — the filter applies
// uniformly to BOTH same-image and cross-image entries. A producer is NOT automatically
// a self-consumer of its own env_provides; if it ever needs to consume its own URL
// (e.g. a genuine same-image-pod case), it must explicitly opt in via env_accepts.
// This ensures the producer's own `env:` declaration (e.g. OLLAMA_HOST=0.0.0.0 baked
// into the image's Dockerfile ENV) is never clobbered by its own env_provides
// service-discovery URL via the quadlet's Environment= directive.
//
//   - Entries are only injected if acceptedEnv[name] is true.
//   - nil acceptedEnv = no filtering (backward compat for remote images without labels).
//   - MCP provides (CHARLY_MCP_SERVERS) are always injected (standard discovery mechanism).
//
// `consumerKey` is the consumer's charly.yml key — base image name (e.g.
// "versa") for the default deploy, or image-with-instance (e.g.
// "versa/ecovoyage") for a named instance. Callers must construct this
// via `deployKey(image, instance)` so cross-instance provides (e.g. another
// instance's AIRFLOW_API_INTERNAL_URL) don't leak into THIS consumer's env.
func (dc *BundleConfig) GlobalEnvForImage(consumerKey, ctrName string, acceptedEnv map[string]bool) []string {
	if dc == nil || dc.Provides == nil {
		return nil
	}
	var result []string

	// Env provides: pod-aware values, filtered uniformly by consumer env_accepts/env_requires.
	// No self-injection bypass — same-deploy entries must be explicitly accepted just like
	// cross-deploy entries.
	for _, entry := range podAwareEnvProvides(dc.Provides.Env, consumerKey, ctrName) {
		if acceptedEnv == nil || acceptedEnv[entry.Name] {
			result = appendOrReplaceEnv(result, entry.Name+"="+entry.Value)
		}
	}

	// MCP provides: pod-aware (always injected — standard discovery mechanism)
	if len(dc.Provides.MCP) > 0 {
		mcpEntries := podAwareMCPProvides(dc.Provides.MCP, consumerKey, ctrName)
		if len(mcpEntries) > 0 {
			mcpJSON, _ := json.Marshal(mcpEntries)
			result = append(result, "CHARLY_MCP_SERVERS="+string(mcpJSON))
		}
	}

	return result
}

// AcceptedEnvSet builds a set of env var names from env_accepts and env_requires declarations.
// Used to filter which env_provides vars get injected into a consumer.
func AcceptedEnvSet(accepts, requires []EnvDependency) map[string]bool {
	m := make(map[string]bool, len(accepts)+len(requires))
	for _, dep := range accepts {
		m[dep.Name] = true
	}
	for _, dep := range requires {
		m[dep.Name] = true
	}
	return m
}

// resolveTemplate replaces template placeholders in a string:
//
//	{{.ContainerName}}        -> containerName
//	{{.ContainerPort <N>}}    -> <N> (literal — kept for symmetry/readability)
//	{{.HostPort <N>}}         -> host port mapped to container port <N>
//	                             (looked up in portMap; falls back to <N>
//	                             if not found — caller should validate the
//	                             port is actually published before relying
//	                             on the substitution)
//
// portMap is a {containerPort -> hostPort} table built from the resolved
// port mapping list at env-injection time. nil portMap is accepted (every
// {{.HostPort N}} degrades to the literal container port — useful for
// validation-time substitution before runtime data is available).
func resolveTemplate(tmpl, containerName string, portMap map[int]int) string {
	out := strings.ReplaceAll(tmpl, "{{.ContainerName}}", containerName)
	out = substPortTemplate(out, "{{.ContainerPort ", "}}", strconv.Itoa)
	out = substPortTemplate(out, "{{.HostPort ", "}}", func(n int) string {
		if portMap != nil {
			if h, ok := portMap[n]; ok {
				return strconv.Itoa(h)
			}
		}
		return strconv.Itoa(n)
	})
	return out
}

// substPortTemplate walks the input, finds every `<prefix><N><suffix>`
// occurrence where N is a numeric argument, and replaces with mapFn(N).
// Unterminated or non-numeric placeholders pass through verbatim — the
// validator (validateProvidesTemplate) rejects them at config time.
func substPortTemplate(s, prefix, suffix string, mapFn func(int) string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		rest := s[i+len(prefix):]
		before, after, ok := strings.Cut(rest, suffix)
		if !ok {
			// unterminated — pass through verbatim
			out.WriteString(prefix)
			s = rest
			continue
		}
		arg := strings.TrimSpace(before)
		if n, err := strconv.Atoi(arg); err == nil {
			out.WriteString(mapFn(n))
		} else {
			out.WriteString(prefix)
			out.WriteString(before)
			out.WriteString(suffix)
		}
		s = after
	}
}

// validateProvidesTemplate checks that only known placeholders are present.
// Allowed:
//
//	{{.ContainerName}}
//	{{.ContainerPort <N>}}   N must parse as a positive integer
//	{{.HostPort <N>}}        N must parse as a positive integer
func validateProvidesTemplate(tmpl string) bool {
	stripped := strings.ReplaceAll(tmpl, "{{.ContainerName}}", "")
	stripped = stripPortTemplate(stripped, "{{.ContainerPort ", "}}")
	stripped = stripPortTemplate(stripped, "{{.HostPort ", "}}")
	return !strings.Contains(stripped, "{{") && !strings.Contains(stripped, "}}")
}

// stripPortTemplate removes every well-formed `<prefix><N><suffix>`
// occurrence where N is a numeric argument. Unterminated or non-numeric
// placeholders are LEFT IN — the outer validator's `{{`/`}}` substring
// check then catches them as invalid.
func stripPortTemplate(s, prefix, suffix string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		rest := s[i+len(prefix):]
		before, after, ok := strings.Cut(rest, suffix)
		if !ok {
			out.WriteString(prefix)
			s = rest
			continue
		}
		arg := strings.TrimSpace(before)
		if _, err := strconv.Atoi(arg); err != nil {
			// non-numeric — leave verbatim so the outer check catches it
			out.WriteString(prefix)
			out.WriteString(before)
			out.WriteString(suffix)
		}
		// numeric N — drop the whole placeholder
		s = after
	}
}

// PortMapFromMappings builds a {containerPort -> hostPort} lookup table
// from the resolved port mapping list. Mappings that don't parse are
// silently skipped (the loud-skip warning lives in CheckPortAvailability).
func PortMapFromMappings(mappings []string) map[int]int {
	if len(mappings) == 0 {
		return nil
	}
	m := make(map[int]int, len(mappings))
	for _, mapping := range mappings {
		p, ok := ParsePortMapping(mapping)
		if !ok {
			continue
		}
		m[p.Container] = p.Host
	}
	return m
}
