package main

// The KERNEL-MANIFEST gate (P16a of the core-minimization program) — the mechanical
// enforcement of CLAUDE.md's "The kernel/plugin boundary law": charly/'s non-test
// file list is PINNED to the CORE-FABRIC allowlist below. A file absent from the
// allowlist is an R-item that leaked into the kernel (an "incomplete seam") and
// FAILS this test; adding a file to the allowlist requires a boundary-law
// justification in the same commit (E envelope / M mechanism / B bootstrap /
// D data — see the plugin skill, "The kernel/plugin boundary law").
//
// Authored RED at program T0 as the residue tracker: the failure output enumerates
// every unowned file so each in-flight cutover (P8b, P11–P15) can shrink the delta.
// It merges LAST (P16), green, and from then on core is mechanically un-growable.
//
// Documented hardware-blocked exceptions (operator-directed, revisitable on GPU
// hardware — C14 of the program plan) are listed in the allowlist with a GPU tag.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// kernelManifestAllowlist is the CORE-FABRIC floor: every non-test .go file the
// kernel may contain, each justified by a boundary-law clause. Patterns are
// anchored regexps over the base filename.
var kernelManifestAllowlist = []string{
	// M — the provider registry + class dispatch (one Provider, transport-invisible).
	`^provider(_.*)?\.go$`,

	// M — plugin load / transports / prescan (the machinery that loads plugins;
	// the one thing that cannot itself be a plugin).
	`^plugin_loader\.go$`, `^plugin_prescan\.go$`, `^plugin_grpc\.go$`,
	`^plugin_transport\.go$`, `^plugin_inproc\.go$`, `^plugin_provider_common\.go$`,
	`^plugin_command_prescan\.go$`, `^plugin_inproc_reverse\.go$`,
	`^plugins_generated\.go$`, // committed pluginsgen output (reproducibility-gated)

	// M — reverse-channel serving + F10 host-callback legs.
	`^plugin_executor_reverse\.go$`, `^plugin_checkcontext_reverse\.go$`,
	`^plugin_dispatch_reverse\.go$`, `^plugin_step_external\.go$`,

	// M — deploy dispatch adapters (the deploy kernel: tree walk, closed transport
	// set, executor composition, substrate lifecycle re-materialization).
	`^deploy_target_external\.go$`, `^substrate_lifecycle_grpc\.go$`,
	`^deploy_target_unified\.go$`, `^unified_targets\.go$`,
	`^deploy_substrate_lifecycle\.go$`, `^deploy_preresolve\.go$`,
	`^deploy_executor_nested\.go$`,

	// B — the bootstrap shell (Kong parse/dispatch spine + prescan wiring) and the
	// registry seed that must exist before any plugin loads.
	`^main\.go$`, `^cli_model_cmd\.go$`, `^registry_bootstrap\.go$`,
	`^reserved_registry\.go$`, `^bootstrap_phase\.go$`,
	`^verb_builtins\.go$`, `^step_builtins\.go$`, `^deploy_builtins\.go$`,

	// M — host reverse-leg serving atoms (kind-blind generic primitives any plugin
	// pulls: venue→executor, endpoint resolve, host HTTP, container exec, CLI reentry).
	`^check_endpoint_resolve\.go$`, `^check_venue\.go$`, `^checkvars\.go$`,
	`^check_http\.go$`, `^checkrun_charly_verbs\.go$`, `^container\.go$`,
	`^host_build_cli\.go$`, `^host_build_config_resolve\.go$`,

	// D — kind-recognition data + freshness/self-identity of the binary itself.
	`^main_freshness\.go$`, `^version\.go$`,

	// GPU (C14) — hardware-blocked fold, operator-directed, revisitable on GPU
	// hardware. These would fold into plugin-gpu under P15 once GPU R10 is possible.
	`^gpu_shim\.go$`, `^gpu_imply\.go$`, `^gpu_allocate\.go$`, `^devices\.go$`,
}

func TestKernelManifest_CoreIsPinnedToTheFabricFloor(t *testing.T) {
	res := make([]*regexp.Regexp, 0, len(kernelManifestAllowlist))
	for _, p := range kernelManifestAllowlist {
		res = append(res, regexp.MustCompile(p))
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var leaked []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		allowed := false
		for _, re := range res {
			if re.MatchString(filepath.Base(name)) {
				allowed = true
				break
			}
		}
		if !allowed {
			leaked = append(leaked, name)
		}
	}
	sort.Strings(leaked)

	if len(leaked) > 0 {
		t.Errorf("KERNEL-MANIFEST gate: %d non-fabric file(s) in charly/ — each is an R-item "+
			"(a concrete kind's schema/shape/validation/behaviour) that belongs in a plugin or "+
			"the SDK, or needs an explicit boundary-law-justified allowlist entry:\n  %s",
			len(leaked), strings.Join(leaked, "\n  "))
	}
}
