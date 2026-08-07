package pod

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestLookupDeployNode guards the 2026-05 fix for `charly update <base> -i <instance>`: the
// deploy lookup must compose the full deploy key (deployKey(image, instance)) so an
// instance-only `<base>/<instance>` entry resolves. Before the fix the dispatcher looked up the
// bare base name and failed with `no deploy named "<base>"`. Relocated from
// charly/update_deploy_dispatch_test.go (K-wave 2 cone CONTESTED) with the function it tests.
func TestLookupDeployNode(t *testing.T) {
	tree := map[string]spec.FleetNode{
		"foo/bar": {Target: "pod", Image: "foo"},
		"baz":     {Target: "pod", Image: "baz"},
		// Nested topology — exercises the dotted-path walk, which must keep
		// working because deployKey returns a dotted name unchanged when
		// instance is empty.
		"stack": {
			Target: "vm",
			Children: map[string]*spec.FleetNode{
				"web": {Target: "pod", Image: "web"},
			},
		},
	}

	t.Run("instance key resolves", func(t *testing.T) {
		node, err := lookupDeployNode(tree, "foo", "bar")
		if err != nil {
			t.Fatalf("instance lookup failed: %v", err)
		}
		if node.Image != "foo" {
			t.Errorf("got Image %q, want foo", node.Image)
		}
	})

	t.Run("bare name resolves", func(t *testing.T) {
		node, err := lookupDeployNode(tree, "baz", "")
		if err != nil {
			t.Fatalf("bare lookup failed: %v", err)
		}
		if node.Image != "baz" {
			t.Errorf("got Image %q, want baz", node.Image)
		}
	})

	t.Run("dotted nested path still walks", func(t *testing.T) {
		node, err := lookupDeployNode(tree, "stack.web", "")
		if err != nil {
			t.Fatalf("nested lookup failed: %v", err)
		}
		if node.Image != "web" {
			t.Errorf("got Image %q, want web", node.Image)
		}
	})

	t.Run("regression: bare base must NOT resolve an instance-only entry", func(t *testing.T) {
		// This is the exact bug — `charly update foo -i bar` previously looked
		// up bare "foo" and found nothing. The fix uses deployKey, so a
		// bare-base lookup (instance "") correctly does NOT match foo/bar.
		_, err := lookupDeployNode(tree, "foo", "")
		if err == nil {
			t.Fatal("bare base 'foo' must not resolve when only foo/bar exists")
		}
	})

	t.Run("missing instance error reports the full key", func(t *testing.T) {
		_, err := lookupDeployNode(tree, "foo", "missing")
		if err == nil {
			t.Fatal("expected error for missing instance key")
		}
		if !strings.Contains(err.Error(), "foo/missing") {
			t.Errorf("error %q should name the full key foo/missing", err.Error())
		}
	})
}

// TestNoteUpdateDisposability guards #30: `charly update` NEVER refuses — it obeys
// any explicit invocation on any target and merely prints a one-line
// transparency note for a non-disposable one. Disposable/ephemeral targets get
// NO note; non-disposable targets get a note naming the key + lifecycle. The
// disposable flag now gates only the AI's autonomous destroy (CLAUDE.md R10) and
// the check-runner's unattended fresh-rebuild — not this human-driven verb.
// Relocated from charly/update_deploy_dispatch_test.go (K-wave 2 cone CONTESTED).
func TestNoteUpdateDisposability(t *testing.T) {
	tDisposable := new(bool)
	*tDisposable = true
	fDisposable := new(bool)
	*fDisposable = false
	cases := []struct {
		name     string
		node     *spec.FleetNode
		image    string
		instance string
		wantNote bool
		want     []string // substrings expected in the note (when wantNote)
	}{
		{
			name:     "explicit disposable true — no note",
			node:     &spec.FleetNode{Disposable: tDisposable},
			image:    "ok-pod",
			wantNote: false,
		},
		{
			name:     "ephemeral implies disposable — no note",
			node:     &spec.FleetNode{Ephemeral: &spec.EphemeralLifetime{}},
			image:    "scratch-pod",
			wantNote: false,
		},
		{
			name:     "absent disposable — note, NOT a refusal",
			node:     &spec.FleetNode{},
			image:    "prod-api",
			wantNote: true,
			want:     []string{"prod-api", "not marked", "disposable: true", "lifecycle: (unset)", "per your explicit"},
		},
		{
			name:     "explicit disposable: false — note",
			node:     &spec.FleetNode{Disposable: fDisposable, Lifecycle: "prod"},
			image:    "locked-api",
			wantNote: true,
			want:     []string{"locked-api", "lifecycle: prod"},
		},
		{
			name:     "instance form includes the slash key",
			node:     &spec.FleetNode{Disposable: fDisposable},
			image:    "versa",
			instance: "ecovoyage",
			wantNote: true,
			want:     []string{"versa/ecovoyage"},
		},
		{
			name:     "lifecycle dev alone — note (charly update still obeys)",
			node:     &spec.FleetNode{Lifecycle: "dev"},
			image:    "dev-bench",
			wantNote: true,
			want:     []string{"dev-bench", "lifecycle: dev"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Capture stderr around the note (the only side effect; it returns
			// nothing and never errors — that IS the point of #30).
			old := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stderr = w
			noteUpdateDisposability(tc.node, tc.image, tc.instance)
			_ = w.Close()
			os.Stderr = old
			b, _ := io.ReadAll(r)
			note := string(b)

			if tc.wantNote {
				if note == "" {
					t.Fatalf("expected a transparency note, got none")
				}
				for _, sub := range tc.want {
					if !strings.Contains(note, sub) {
						t.Errorf("note %q missing substring %q", note, sub)
					}
				}
			} else if note != "" {
				t.Errorf("expected NO note for a disposable target, got %q", note)
			}
		})
	}
}
