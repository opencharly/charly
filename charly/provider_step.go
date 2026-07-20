package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// StepProvider is the typed in-process form of an InstallStep Provider: it emits one step
// to the ONE remaining in-proc venue — the OCI image build (the pod-overlay add_candy:
// Containerfile synthesis). Every InstallStep kind implements it; the dispatch resolves the
// step through providerRegistry.ResolveStep(step.Kind()) and calls EmitOCI, which preserves
// the build venue's exact behaviour.
//
// There is NO EmitLocal and NO EmitVM: BOTH target:local AND target:vm externalized (into
// candy/plugin-deploy-local / candy/plugin-deploy-vm), whose out-of-process kit.WalkPlans
// executes every step on the venue (the plugin-renderable kinds via the F2 reverse legs,
// the host-engine kinds via RunHostStep) — so the in-proc per-deploy-step dispatch is gone.
// The sole remaining in-proc StepProvider consumer is the pod-overlay step dispatch
// (charly/oci_step_emit.go's ociEmitStep — the host "oci-emit-step" render-seam handler; the
// deploykit.OCITarget WALKER itself lives in sdk/deploykit as of P11c, with no in-core instance).
type StepProvider interface {
	Provider
	// EmitOCI renders one step's BUILD-venue (pod-overlay Containerfile) fragment. It RETURNS
	// the fragment (the caller splices it) rather than writing to an the walker buffer — the
	// deploykit.OCITarget walker now lives in sdk/deploykit (P11c) and the in-core dispatch has no
	// deploykit.OCITarget instance, so returning the string decouples the StepProvider from the buffer
	// (the former t.buf.WriteString). build carries the host-side buildEngineContext (Box for
	// the plugin verb's distros, etc.) the host reconstructs from the cached overlay Generator.
	// The fragment includes any trailing newline; an empty return is a no-op (a deploy-only
	// step records nothing).
	EmitOCI(step spec.InstallStep, plan *deploykit.InstallPlan, build buildEngineContext) (string, error)
}

// builtinStepBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in step provider.
type builtinStepBase struct{}

func (builtinStepBase) Class() ProviderClass { return ClassStep }
func (builtinStepBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in install step is in-process only (no out-of-proc Invoke)")
}

// stepProviderFor resolves an InstallStep kind to its StepProvider.
func stepProviderFor(kind spec.StepKind) (StepProvider, bool) {
	prov, ok := providerRegistry.ResolveStep(string(kind))
	if !ok {
		return nil, false
	}
	sp, ok := prov.(StepProvider)
	return sp, ok
}

// allStepKinds is the fixed InstallStep IR vocabulary (Go-internal; steps are not a
// user-authored CUE vocab). EVERY kind here round-trips through stepToView/stepFromView (the
// deploy view, exercised by step_view_test); the bijection below asserts each kind is SERVED —
// either by a compiled-in in-proc StepProvider, or (for the pluginEmitStepWords set) by a
// compiled-in class:step plugin's build-emit.

// pluginEmitStepWords maps the builtin InstallStep kinds whose BUILD-emit externalized to the
// lowercase-hyphenated class:step plugin word that serves their pod-overlay OpEmit
// (candy/plugin-installstep). These kinds have NO in-proc StepProvider — ociEmitStep routes
// them here, serializing the step VIEW as the OpEmit payload. Their DEPLOY leg is unchanged
// (sdk/kit.WalkPlans renders them from the same view; reboot's is the host-side guest
// reboot over RunHostStep → rebootVenueAndWait). apk-install's and reboot's plugin declare
// Emits=false (no build fragment); every other word Emits=true.
//
// Two sub-categories, distinguished by whether the OpEmit render needs the host build engine:
//   - PURE (C1.1 + C1.6): file/shell-hook/shell-snippet/service-packaged/service-custom/repo-change/
//     apk-install (C1.1) + reboot (C1.6) — the plugin formats the fragment directly from the step
//     VIEW. apk-install and reboot are the NO-OP-emit members (Emits=false, empty fragment): an
//     image build installs no apk / reboots nothing.
//   - HOST-COUPLED (C1.2/C1.3/C1.4/C1.5): system-packages (C1.2) + builder (C1.3) +
//     local-pkg-install (C1.4) + op (C1.5) — the plugin's OpEmit calls back the host's "step-emit"
//     host-builder (HostBuild) for a render it cannot do across the process boundary
//     (system-packages needs the DistroDef format templates; builder needs the multi-stage
//     buildStageContext + RenderTemplate engine; local-pkg-install calls deploykit's pure
//     RenderLocalPkgImageInstall/BuildLocalPkgOnHost + host-dir staging; op needs the
//     RICHEST Generator.emitTasks per-verb render pipeline — COPY staging + op coalescing). See
//     charly/step_emit_hostbuild.go (stepEmitSystemPackages, stepEmitBuilder,
//     stepEmitLocalPkgInstall, stepEmitOp).
var pluginEmitStepWords = map[spec.StepKind]string{
	spec.StepKindFile:            "file",
	spec.StepKindShellHook:       "shell-hook",
	spec.StepKindShellSnippet:    "shell-snippet",
	spec.StepKindServicePackaged: "service-packaged",
	spec.StepKindServiceCustom:   "service-custom",
	spec.StepKindRepoChange:      "repo-change",
	spec.StepKindApkInstall:      "apk-install",
	spec.StepKindReboot:          "reboot",
	spec.StepKindSystemPackages:  "system-packages",
	spec.StepKindBuilder:         "builder",
	spec.StepKindLocalPkgInstall: "local-pkg-install",
	spec.StepKindOp:              "op",
}

// checkStepProviderBijection asserts every InstallStep kind is SERVED. A kind in
// pluginEmitStepWords must resolve to a compiled-in class:step plugin declaring a StepContract
// (its build-emit); every other kind must resolve to an in-proc StepProvider (its EmitOCI). Run in
// the same init() that registers, after registration (the compiled-in plugins register first —
// plugins_generated.go's init precedes registry_bootstrap.go's alphabetically, the SAME ordering
// checkVerbProviderBijection relies on).
func checkStepProviderBijection() error {
	var missing []string
	for _, k := range deploykit.AllStepKinds {
		if word, isPlugin := pluginEmitStepWords[k]; isPlugin {
			p, ok := providerRegistry.resolve(ClassStep, word)
			if !ok {
				missing = append(missing, fmt.Sprintf("%s (externalized build-emit; class:step plugin %q not registered)", k, word))
				continue
			}
			if _, ok := p.(stepContractCarrier); !ok {
				missing = append(missing, fmt.Sprintf("%s (class:step provider %q declares no StepContract)", k, word))
			}
			continue
		}
		p, ok := providerRegistry.resolve(ClassStep, string(k))
		if !ok {
			missing = append(missing, string(k))
			continue
		}
		if _, ok := p.(StepProvider); !ok {
			missing = append(missing, string(k)+" (registered but not a StepProvider)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: unserved InstallStep kinds: %v", missing)
	}
	return nil
}
