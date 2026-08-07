package fleet

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// refs_test.go — relocated (in part) from charly/refs_test.go (#55 decoupling, Batch A):
// these 4 tests assert deploykit.CandyRef/StripVersion/BareRef/IsRemoteCandyRef directly, zero
// charly coupling. TestPickCandyVersion is Batch C's concern (plugin-loader,
// loaderkit.PickCandyVersion); TestParseRemoteRef/TestIsRemoteImageRef and the ~9 white-box
// charly refs.go/layers.go remote-ref-collection-engine tests all stay in charly.

func TestCandyRef(t *testing.T) {
	tests := []struct {
		raw      string
		bare     string
		version  string
		isRemote bool
	}{
		{"python", "python", "", false},
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda", "v1.0.0", true},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda", "", true},
	}
	for _, tt := range tests {
		r := deploykit.CandyRef{Raw: tt.raw}
		if got := r.Bare(); got != tt.bare {
			t.Errorf("CandyRef{%q}.Bare() = %q, want %q", tt.raw, got, tt.bare)
		}
		if got := r.Version(); got != tt.version {
			t.Errorf("CandyRef{%q}.Version() = %q, want %q", tt.raw, got, tt.version)
		}
		if got := r.IsRemote(); got != tt.isRemote {
			t.Errorf("CandyRef{%q}.IsRemote() = %v, want %v", tt.raw, got, tt.isRemote)
		}
	}
	// A resolved sibling key overrides Bare() but leaves Raw (and thus the
	// transitive-fetch view) intact.
	r := deploykit.CandyRef{Raw: "ffmpeg", Resolved: "github.com/org/repo/layers/ffmpeg"}
	if r.Bare() != "github.com/org/repo/layers/ffmpeg" {
		t.Errorf("resolved Bare() = %q", r.Bare())
	}
	if r.Raw != "ffmpeg" {
		t.Errorf("resolved must leave Raw intact, got %q", r.Raw)
	}
}

func TestStripVersion(t *testing.T) {
	tests := []struct {
		ref     string
		wantRef string
		wantVer string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "@github.com/org/repo/layers/cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/cuda:main", "@github.com/org/repo/layers/cuda", "main"},
		{"@github.com/org/repo/layers/cuda", "@github.com/org/repo/layers/cuda", ""},
		{"pixi", "pixi", ""},
		{"my-layer", "my-layer", ""},
	}

	for _, tt := range tests {
		gotRef, gotVer := deploykit.StripVersion(tt.ref)
		if gotRef != tt.wantRef || gotVer != tt.wantVer {
			t.Errorf("StripVersion(%q) = (%q, %q), want (%q, %q)", tt.ref, gotRef, gotVer, tt.wantRef, tt.wantVer)
		}
	}
}

func TestBareRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda"},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda"},
		{"pixi", "pixi"},
		{"my-layer", "my-layer"},
	}

	for _, tt := range tests {
		got := deploykit.BareRef(tt.ref)
		if got != tt.want {
			t.Errorf("BareRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestIsRemoteCandyRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"pixi", false},
		{"my-layer", false},
		{"@github.com/org/repo/layers/cuda", true},
		{"@github.com/opencharly/charly/layers/cuda", true},
		{"@gitlab.com/org/repo/layers/cuda", true},
		{"@github.com/org/repo/layers/cuda:v1.0.0", true},
		{"github.com/org/repo/layers/cuda", false}, // no @ prefix = not remote
	}

	for _, tt := range tests {
		got := deploykit.IsRemoteCandyRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteCandyRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}
