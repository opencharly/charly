package main

import (
	"testing"

	"github.com/opencharly/spec/checkstep"
	"github.com/opencharly/spec/spec"
)

// relocatedVerbWiring is one compiled-in kit candy verb's WIRING contract: the verb
// resolves through providerRegistry and is registered with the roles its candy
// implements (check / act / typed-step). The BEHAVIOR half — the verb's check/act/step
// semantics — lives beside each verb in its own candy/plugin-<verb> module (the
// tests-move-with-subjects doctrine); these tests prove only that the dispatch reaches
// the plugin (the kernel/plugin boundary law's wiring half). The former
// plugin_*_relocated_test.go family asserted BOTH halves from core; the behavior half
// relocated to the owning candies, leaving this wiring contract behind.
type relocatedVerbWiring struct {
	verb     string
	check    bool // resolves as a CheckVerbProvider
	act      bool // resolves as a ProvisionActor
	step     bool // resolves as a TypedStepProvider
	stepKind spec.StepKind
}

func TestRelocatedVerbWiring(t *testing.T) {
	cases := []relocatedVerbWiring{
		{"addr", true, false, false, ""},
		{"command", true, false, false, ""},
		{"dns", true, false, false, ""},
		{"examplerunverb", true, false, false, ""},
		{"file", true, true, false, ""},
		{"interface", true, false, false, ""},
		{"kernel-param", true, true, false, ""},
		{"mount", true, true, false, ""},
		{"package", true, true, true, spec.StepKindSystemPackages},
		{"process", true, false, false, ""},
		{"service", true, true, true, spec.StepKindServicePackaged},
		{"unix_group", true, true, false, ""},
		{"user", true, true, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			prov, ok := providerRegistry.ResolveVerb(tc.verb)
			if !ok {
				t.Fatalf("%s verb not registered — compiled-in kit candy (candy/plugin-%s) failed", tc.verb, tc.verb)
			}
			if tc.check {
				if _, ok := prov.(CheckVerbProvider); !ok {
					t.Fatalf("%s provider is not a CheckVerbProvider: %T", tc.verb, prov)
				}
			}
			if tc.act {
				if _, ok := prov.(ProvisionActor); !ok {
					t.Fatalf("%s provider is not a ProvisionActor: %T", tc.verb, prov)
				}
			}
			if tc.step {
				sp, ok := prov.(TypedStepProvider)
				if !ok {
					t.Fatalf("%s provider is not a TypedStepProvider: %T", tc.verb, prov)
				}
				if sp.LowersTo() != tc.stepKind {
					t.Fatalf("%s LowersTo = %v, want %v", tc.verb, sp.LowersTo(), tc.stepKind)
				}
			}
		})
	}
}

// TestRelocatedMatchingVerb_Wiring proves the `matching` verb — a compiled-in pb-shape
// candy (candy/plugin-matching) — resolves through providerRegistry as a plain Provider
// (the pb Invoke envelope, NOT a typed in-proc CheckVerbProvider). Its matcher-evaluation
// behavior is covered by candy/plugin-matching/plugin_test.go.
func TestRelocatedMatchingVerb_Wiring(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("matching")
	if !ok {
		t.Fatal("matching verb not registered — compiled-in pb candy (candy/plugin-matching) failed")
	}
	if _, isCheck := prov.(CheckVerbProvider); isCheck {
		t.Fatalf("matching provider is a CheckVerbProvider (%T), want a plain pb Provider", prov)
	}
}

// TestMaterializeStep proves the core adapter's materialization of a kit candy's
// checkstep.StepDescriptor into the real InstallPlan step — the Format/Phase/cross-distro
// name resolution the host materializer performs (kitVerbActStepAdapter.ConstructStep →
// materializeStep). This is a CORE mechanism (the adapter), so its test stays in core;
// the candy-side half (StepKind + ConstructStepDescriptor decoding plugin_input) is
// covered by candy/plugin-package + candy/plugin-service's own tests.
func TestMaterializeStep(t *testing.T) {
	// SystemPackages: image format + install phase + the cross-distro-resolved name.
	step := materializeStep(checkstep.StepDescriptor{
		SystemPackages: &checkstep.SystemPackagesDesc{Package: "openssh", PackageMap: map[string]string{"fedora": "openssh-server"}},
	}, stepConstructCtx{CandyName: "net", PkgFormat: "rpm", DistroTags: []string{"fedora:43", "fedora"}})
	sps, ok := step.(*spec.SystemPackagesStep)
	if !ok {
		t.Fatalf("materializeStep returned %T, want *SystemPackagesStep", step)
	}
	if sps.Format != "rpm" || sps.Phase != spec.PhaseInstall || len(sps.Packages) != 1 || sps.Packages[0] != "openssh-server" {
		t.Fatalf("SystemPackagesStep = %+v, want Format=rpm Phase=Install Packages=[openssh-server] (cross-distro map applied)", sps)
	}

	// ServicePackaged: unit + enable + the candy name.
	step2 := materializeStep(checkstep.StepDescriptor{
		ServicePackaged: &checkstep.ServicePackagedDesc{Unit: "nginx", Enable: true},
	}, stepConstructCtx{CandyName: "mylayer"})
	sps2, ok := step2.(*spec.ServicePackagedStep)
	if !ok {
		t.Fatalf("materializeStep returned %T, want *ServicePackagedStep", step2)
	}
	if sps2.Unit != "nginx" || !sps2.Enable || sps2.CandyName != "mylayer" {
		t.Fatalf("ServicePackagedStep = %+v, want Unit=nginx Enable=true CandyName=mylayer", sps2)
	}
}
