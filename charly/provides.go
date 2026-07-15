package main

import (
	"strconv"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// MCPProvideEntry is a resolved mcp_provides entry. It lives in sdk/spec (shared with the
// out-of-process mcp check verb via spec.PodAwareMCPProvides, R3); aliased here for charly's
// deploy-time provides pipeline. Its GetName/GetSource (in spec) satisfy deploykit.Named
// structurally.
type MCPProvideEntry = spec.MCPProvideEntry

// filterOwnProvides removes entries injected by the given image (self-exclusion).
// NOTE: No longer used in GlobalEnvForImage (replaced by podAwareEnvProvides).
// Kept for deploykit.RemoveBySource and other callers that need strict exclusion.
func filterOwnProvides[T deploykit.Named](entries []T, boxName string) []T {
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
// bug: `deploykit.IsSameBaseBox("versa/ecovoyage", "versa")` returns true (deletion
// semantics), which would let another instance's env_provides leak into the
// base consumer's runtime env and trigger a second-order failure when
// strings.ReplaceAll("charly-versa-ecovoyage", "charly-versa", "localhost") produces
// the malformed hostname "localhost-ecovoyage". Exact match is correct.

// removeBySource / removeByExactSource + the Named interface + IsSameBaseBox moved to
// sdk/deploykit (deploykit.RemoveBySource / deploykit.RemoveByExactSource / deploykit.Named /
// deploykit.IsSameBaseBox) — shared with the deploy state-model body relocated in K5-Unit-1.
// filterOwnProvides above uses deploykit.Named; the deploy state clean path
// (deploykit.CleanDeployEntry) calls deploykit.RemoveBySource/RemoveByExactSource directly.

// podAwareMCPProvides moved to sdk/spec (spec.PodAwareMCPProvides), shared with the
// out-of-process mcp check verb (R3); see the same rationale as podAwareEnvProvides above.

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
