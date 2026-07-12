package vm

import (
	"strings"

	"github.com/opencharly/sdk/vmshared"
)

// vm_util_shims.go — small host-side helpers the moved VM CLI handlers (P10) reference by their
// former core short names. These carry NO core coupling: currentUsername + the host-distro install
// hint come from sdk/vmshared, dedupeNonEmpty is a pure set helper, and gatherResources reaches the
// host config-resolve seam (the resolved resource vocabulary the loader owns).

// currentUsername returns $USER (or the "charly" fallback) — vmshared owns the one implementation
// (ssh_target.go), exported as vmshared.CurrentUsername; aliased here so the moved handlers compile.
var currentUsername = vmshared.CurrentUsername

// dedupeNonEmpty trims + dedups a token list (GPU auto-allocation computes the claimant's tokens).
// Copied verbatim from the former charly/preempt.go — a pure set helper, no core dependency.
func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// gatherResources returns the project's resolved resource vocabulary (token -> ResolvedResource,
// carrying the GPU selector that drives auto-allocation). The loader is a core Mechanism the plugin
// cannot hold, so this reaches the host config-resolve seam with an empty entity (resolveResources()
// is entity-independent). nil on a project-less invocation or a resolve error — the former core
// gatherResources's "nil when none / unreadable" contract.
func gatherResources() map[string]*ResolvedResource {
	reply, err := hostConfigResolve("")
	if err != nil {
		return nil
	}
	return reply.Resources
}

// InstallHint returns a best-effort, host-distro-appropriate install command for a missing binary
// (machine.go's gvproxy-not-found message). The curated per-binary package-name table stays a core
// Mechanism (`charly doctor`, embedded charly.yml install_hints:); the VM plugin has no project
// loader, so it emits the package-manager form derived from the live host distro (sdk/vmshared) —
// enough for an error hint, with no core coupling.
func InstallHint(binary string) string {
	if hd, err := vmshared.DetectHostDistro(); err == nil && hd != nil {
		switch hd.FormatHint() {
		case "rpm":
			return "sudo dnf install " + binary
		case "deb":
			return "sudo apt install " + binary
		case "pac":
			return "sudo pacman -S " + binary
		}
	}
	return "install " + binary + " via your package manager"
}
