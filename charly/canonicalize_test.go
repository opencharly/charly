package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
)

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

// TestResolveLocalImageRef_PrefersBaseOverAlias asserts that when two
// equal-CalVer candidates share the `ai.opencharly.box` label
// (because `tagDeployAlias` tags an instance alias inheriting the
// base label), the resolver picks the BASE ref (repo's trailing
// segment == short name) over the alias (`<base>/<instance>`).
func TestResolveLocalImageRef_PrefersBaseOverAlias(t *testing.T) {
	// matchesShortName logic exercised via the sort callback's
	// behavior. We can't run the full resolver without podman, but
	// we verify the helper directly by simulating the candidates.
	// The actual matchesShortName closure lives inside
	// resolveLocalImageRef; here we mirror it.
	matchesShortName := func(ref, name string) bool {
		repo := ref
		for i, ch := range ref {
			if ch == ':' || ch == '@' {
				repo = ref[:i]
				break
			}
		}
		if i := lastIndex(repo, '/'); i >= 0 {
			repo = repo[i+1:]
		}
		return repo == name
	}
	for _, tc := range []struct {
		ref, name string
		want      bool
	}{
		{"ghcr.io/opencharly/versa:2026.132.1941", "versa", true},
		{"ghcr.io/opencharly/versa/ecovoyage:2026.132.1941", "versa", false},
		{"ghcr.io/opencharly/sway-browser-vnc:1.0", "sway-browser-vnc", true},
		{"ghcr.io/opencharly/sway-browser-vnc/ecovoyage:1.0", "sway-browser-vnc", false},
	} {
		if got := matchesShortName(tc.ref, tc.name); got != tc.want {
			t.Errorf("matchesShortName(%q, %q) = %v, want %v", tc.ref, tc.name, got, tc.want)
		}
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
	dc := &deploykit.BundleConfig{
		Bundle: map[string]spec.BundleNode{
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
	dc.Bundle["selkies/work"] = spec.BundleNode{Image: "selkies", ResolvedPort: []string{"3001:3000"}}
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

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
