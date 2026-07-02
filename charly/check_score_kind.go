package main

// check_score_kind.go — the `iterate:` block (the AI-loop orchestration).
//
// The AI-loop orchestration — the agent list, plateau policy, prompt,
// sandbox, MCP endpoint, notes toggle, env — lives in an `IterateConfig`
// carried on a deploy node (a `disposable: true` deploy is a check bed) (BundleNode.Iterate).
// `charly check run <entity>`: when the entity carries an `iterate:` block the
// AI loop drives it (scoring the entity's own `plan:` check:/agent-check:
// steps); otherwise the entity is a plain check bed (a `disposable: true` deploy) and runs the
// deterministic R10 sequence.
//
// The scored content is the entity's OWN `plan:` (baked + include:'d + inline)
// — there is no separate catalog to resolve.

// EffectiveMCPEndpoint resolves the canonical ${MCP_ENDPOINT} value.
//
//   - nil pointer (unset)        → DefaultMCPEndpoint
//   - non-nil, value=""          → "" (disabled by author)
//   - non-nil, value=<something> → that value verbatim
func iterateEffectiveMCPEndpoint(i *IterateConfig) string {
	if i == nil || i.MCPEndpoint == nil {
		return DefaultMCPEndpoint
	}
	return *i.MCPEndpoint
}

// DefaultMCPEndpoint is the canonical charly-mcp bind URL.
const DefaultMCPEndpoint = "http://localhost:18765/mcp"

// ---------------------------------------------------------------------------
// Target discriminator
// ---------------------------------------------------------------------------

// TargetKind identifies which executor backend the iterate sandbox uses.
type TargetKind string

const (
	TargetKindPod  TargetKind = "pod"
	TargetKindVM   TargetKind = "vm"
	TargetKindHost TargetKind = "host"
)

// ResolveIterateSandbox classifies the iterate sandbox's target kind by
// looking up the named deployment in the project's Bundle map. A bare host
// sandbox (target: local with host: local) resolves to TargetKindHost; an
// explicit vm: deploy → TargetKindVM; everything else (the default pod) →
// TargetKindPod. The returned name is the sandbox deploy name (empty for a
// host sandbox).
func ResolveIterateSandbox(uf *UnifiedFile, sandbox string) (TargetKind, string) {
	if sandbox == "" {
		return TargetKindHost, ""
	}
	if uf != nil {
		// A check bed is a disposable bundle in Deploy, so one lookup covers
		// both deploys and beds.
		if node, ok := uf.Bundle[sandbox]; ok {
			return targetKindForNode(&node), sandbox
		}
	}
	return TargetKindPod, sandbox
}

// targetKindForNode maps a BundleNode's target to a TargetKind.
func targetKindForNode(node *BundleNode) TargetKind {
	switch node.Target {
	case "vm":
		return TargetKindVM
	case "local", "host":
		return TargetKindHost
	default:
		return TargetKindPod
	}
}
