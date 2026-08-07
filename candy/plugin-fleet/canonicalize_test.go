package fleet

import (
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// canonicalize_test.go — relocated (in part) from charly/canonicalize_test.go (#55
// decoupling, Batch A): TestCanonicalizeDeployArg + the 2 TestMergeDeployOntoMetadata_ tests
// assert deploykit.CanonicalizeDeployArg/MergeDeployOntoMetadata directly, zero charly dep.
// The base-over-alias resolution preference this header used to point at ("a local
// matchesShortName mirror closure ... stays in charly") is no longer covered here or in charly:
// that closure was a sort TIEBREAK, and it has been promoted to a candidate FILTER inside
// container.ResolveLocalImageRef itself. Its witness is
// TestResolveLocalImageRef_NeverReturnsSiblingDeployAlias (spec/container/local_image_coneb_test.go),
// which asserts the stronger property — an untagged resolve of a box never returns ANOTHER
// deployment's alias, not merely that the base wins an exact CalVer tie.

// TestCanonicalizeDeployArg exercises the Pattern A "<base>/<instance>"
// splitting that every command entry point applies. Regression guard
// against the 2026-05-12 bug class where Pattern A keys leaked past
// the canonicalization boundary and downstream MergeDeployOntoMetadata
// looked up the wrong deploy.yml key (dropping port/env overlays).
func TestCanonicalizeDeployArg(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arg       string
		instance  string
		wantImage string
		wantInst  string
	}{
		{"pattern_A_split", "versa/ecovoyage", "", "versa", "ecovoyage"},
		{"pattern_A_three_segments_NOT_split", "ghcr.io/owner/img", "", "ghcr.io/owner/img", ""}, // registry host
		{"pattern_B_fq_ref", "ghcr.io/opencharly/versa:2026.132.1941", "", "ghcr.io/opencharly/versa:2026.132.1941", ""},
		{"pattern_B_digest", "ghcr.io/x/y@sha256:abc", "", "ghcr.io/x/y@sha256:abc", ""},
		{"bare_short_name", "versa", "", "versa", ""},
		{"explicit_instance_passthrough", "versa", "ecovoyage", "versa", "ecovoyage"},
		{"explicit_instance_wins_over_slash", "versa/dev", "prod", "versa/dev", "prod"}, // operator chose -i; don't override
		{"empty", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotImage, gotInst := deploykit.CanonicalizeDeployArg(tc.arg, tc.instance)
			if gotImage != tc.wantImage || gotInst != tc.wantInst {
				t.Errorf("canonicalizeDeployArg(%q, %q) = (%q, %q), want (%q, %q)",
					tc.arg, tc.instance, gotImage, gotInst, tc.wantImage, tc.wantInst)
			}
		})
	}
}

// TestMergeDeployOntoMetadata_KeyedByDeployNameNotImage guards the bug class
// where MergeDeployOntoMetadata looked up the deploy overlay by meta.Box (the
// baked ai.opencharly.box short-name) instead of the caller's deploy key. A
// kind:check bed (key "check-cachyos-ollama-pod", image "ollama") that remaps
// 45434:11434 MUST keep its own port even when a sibling production deploy keyed
// "ollama" publishes the image-default 11434 — otherwise the bed's quadlet
// inherits 11434 and collides with the running same-image service at start
// (rootlessport "address already in use"). Fails against the pre-fix code, which
// keyed on meta.Box and therefore returned 11434 for the bed too.
func TestMergeDeployOntoMetadata_KeyedByDeployNameNotImage(t *testing.T) {
	// A deploy's published ports are its persisted ResolvedPort (the auto-
	// allocated / pinned host:container mapping charly config computed). The merge
	// applies the entry resolved BY DEPLOY KEY — never a sibling's, never the
	// image-label container ports.
	dc := &deploykit.FleetConfig{
		Fleet: map[string]spec.FleetNode{
			"ollama":                   {ResolvedPort: []string{"11434:11434"}},
			"check-cachyos-ollama-pod": {Image: "ollama", ResolvedPort: []string{"45434:11434"}},
		},
	}

	// Bed: deploy key differs from the baked image short-name. The merge must
	// resolve the bed's OWN ResolvedPort, not the sibling "ollama" deploy.
	bedMeta := &spec.BoxMetadata{Box: "ollama", Port: []string{"11434"}}
	deploykit.MergeDeployOntoMetadata(bedMeta, dc, "check-cachyos-ollama-pod", "")
	if len(bedMeta.Port) != 1 || bedMeta.Port[0] != "45434:11434" {
		t.Errorf("bed merge: got Ports=%v, want [45434:11434] (must not pick up sibling 'ollama' deploy or the image default)", bedMeta.Port)
	}

	// Plain deploy: key == image short-name. Resolves its own entry as before.
	plainMeta := &spec.BoxMetadata{Box: "ollama", Port: []string{"11434"}}
	deploykit.MergeDeployOntoMetadata(plainMeta, dc, "ollama", "")
	if len(plainMeta.Port) != 1 || plainMeta.Port[0] != "11434:11434" {
		t.Errorf("plain merge: got Ports=%v, want [11434:11434]", plainMeta.Port)
	}

	// Instance deploy: "<base>/<instance>" key form resolves correctly.
	dc.Fleet["selkies/work"] = spec.FleetNode{Image: "selkies", ResolvedPort: []string{"3001:3000"}}
	instMeta := &spec.BoxMetadata{Box: "selkies", Port: []string{"3000"}}
	deploykit.MergeDeployOntoMetadata(instMeta, dc, "selkies", "work")
	if len(instMeta.Port) != 1 || instMeta.Port[0] != "3001:3000" {
		t.Errorf("instance merge: got Ports=%v, want [3001:3000]", instMeta.Port)
	}
}

// TestMergeDeployOntoMetadata_VolumesScopedToDeployKey pins the GENERIC
// guarantee the operator asked for: EVERY distinctly-named deploy of an image —
// the base deploy, a second production pod (Pattern-B), an instance, or a
// kind:check bed — gets volume mounts under its OWN deploy/container name, so no
// two differently-named pods ever share a named volume (the immich-pgdata
// sharing incident). The ONLY no-op is the base deploy whose key == image
// (nothing else can share that name), so that single deploy's names never
// change (zero migration). Keyed by deployVolumePrefix == container name.
func TestMergeDeployOntoMetadata_VolumesScopedToDeployKey(t *testing.T) {
	const vol = "charly-immich-ml-pgdata"
	mk := func() *spec.BoxMetadata {
		return &spec.BoxMetadata{Box: "immich-ml", Volume: []deploykit.VolumeMount{{VolumeName: vol, ContainerPath: "/data"}}}
	}
	for _, tc := range []struct {
		name       string
		deployName string
		instance   string
		want       string
	}{
		{"base_deploy_key_equals_image_unchanged", "immich-ml", "", "charly-immich-ml-pgdata"},
		{"second_production_pod_same_image_isolated", "immich-prod", "", "charly-immich-prod-pgdata"},
		{"instance_isolated", "immich-ml", "blue", "charly-immich-ml-blue-pgdata"},
		{"check_bed_isolated", "check-cachyos-immich-ml-pod", "", "charly-check-cachyos-immich-ml-pod-pgdata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := mk()
			// nil dc → exercises the unconditional, overlay-independent re-scope.
			deploykit.MergeDeployOntoMetadata(meta, nil, tc.deployName, tc.instance)
			if got := meta.Volume[0].VolumeName; got != tc.want {
				t.Errorf("deploy %q/%q: volume = %q, want %q", tc.deployName, tc.instance, got, tc.want)
			}
		})
	}
}
