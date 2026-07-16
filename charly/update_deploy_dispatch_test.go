package main

import (
	"github.com/opencharly/sdk/vmshared"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveUpdateDeployNode guards the 2026-05 fix for `charly update <base>
// -i <instance>`: the deploy lookup must compose the full deploy key
// (deployKey(image, instance)) so an instance-only `<base>/<instance>`
// entry resolves. Before the fix the dispatcher looked up the bare base
// name and failed with `no deploy named "<base>"`.
func TestResolveUpdateDeployNode(t *testing.T) {
	tree := map[string]BundleNode{
		"foo/bar": {Target: "pod", Image: "foo"},
		"baz":     {Target: "pod", Image: "baz"},
		// Nested topology — exercises the dotted-path walk, which must keep
		// working because deployKey returns a dotted name unchanged when
		// instance is empty.
		"stack": {
			Target: "vm",
			Children: map[string]*BundleNode{
				"web": {Target: "pod", Image: "web"},
			},
		},
	}

	t.Run("instance key resolves", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "foo", "bar")
		if err != nil {
			t.Fatalf("instance lookup failed: %v", err)
		}
		if node.Image != "foo" {
			t.Errorf("got Image %q, want foo", node.Image)
		}
	})

	t.Run("bare name resolves", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "baz", "")
		if err != nil {
			t.Fatalf("bare lookup failed: %v", err)
		}
		if node.Image != "baz" {
			t.Errorf("got Image %q, want baz", node.Image)
		}
	})

	t.Run("dotted nested path still walks", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "stack.web", "")
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
		_, err := resolveUpdateDeployNode(tree, "foo", "")
		if err == nil {
			t.Fatal("bare base 'foo' must not resolve when only foo/bar exists")
		}
	})

	t.Run("missing instance error reports the full key", func(t *testing.T) {
		_, err := resolveUpdateDeployNode(tree, "foo", "missing")
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
func TestNoteUpdateDisposability(t *testing.T) {
	tDisposable := new(true)
	fDisposable := new(false)
	cases := []struct {
		name     string
		node     *BundleNode
		image    string
		instance string
		wantNote bool
		want     []string // substrings expected in the note (when wantNote)
	}{
		{
			name:     "explicit disposable true — no note",
			node:     &BundleNode{Disposable: tDisposable},
			image:    "ok-pod",
			wantNote: false,
		},
		{
			name:     "ephemeral implies disposable — no note",
			node:     &BundleNode{Ephemeral: &vmshared.EphemeralLifetime{}},
			image:    "scratch-pod",
			wantNote: false,
		},
		{
			name:     "absent disposable — note, NOT a refusal",
			node:     &BundleNode{},
			image:    "prod-api",
			wantNote: true,
			want:     []string{"prod-api", "not marked", "disposable: true", "lifecycle: (unset)", "per your explicit"},
		},
		{
			name:     "explicit disposable: false — note",
			node:     &BundleNode{Disposable: fDisposable, Lifecycle: "prod"},
			image:    "locked-api",
			wantNote: true,
			want:     []string{"locked-api", "lifecycle: prod"},
		},
		{
			name:     "instance form includes the slash key",
			node:     &BundleNode{Disposable: fDisposable},
			image:    "versa",
			instance: "ecovoyage",
			wantNote: true,
			want:     []string{"versa/ecovoyage"},
		},
		{
			name:     "lifecycle dev alone — note (charly update still obeys)",
			node:     &BundleNode{Lifecycle: "dev"},
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

// TestExtractQuadletImageLine guards the 2026-05-26 cross-pollution
// fix on updateAllDeployedQuadlets. The function preserves the
// operator-chosen Image= line on a sibling deploy when an unrelated
// `charly update <bed>` triggers a cross-deploy env refresh; the test
// covers the happy path (Image= present), the absent-Image= path
// (caller falls back to fresh resolution), and the missing-file path
// (caller falls back).
func TestExtractQuadletImageLine(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name: "Image= present at top of [Container] block",
			content: `[Unit]
Description=x

[Container]
Image=ghcr.io/opencharly/versa:2026.135.1326
ContainerName=charly-versa-ecovoyage
`,
			want: "ghcr.io/opencharly/versa:2026.135.1326",
		},
		{
			name: "Image= with sidecar Pod= directive (still finds the right line)",
			content: `[Container]
Pod=charly-versa.pod
Image=ghcr.io/tailscale/tailscale:latest
ContainerName=charly-versa-tailscale
`,
			want: "ghcr.io/tailscale/tailscale:latest",
		},
		{
			name: "no Image= line returns empty without error (caller falls back)",
			content: `[Unit]
Description=missing-image

[Container]
ContainerName=charly-broken
`,
			want: "",
		},
		{
			name:    "missing file returns error",
			content: "", // signal: don't create file
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "test-"+tc.name+".container")
			if tc.content != "" || tc.name != "missing file returns error" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got, err := extractQuadletImageLine(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("imageRef = %q, want %q", got, tc.want)
			}
		})
	}
}
