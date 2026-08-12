package box

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// captureStdout redirects os.Stdout for the duration of fn and returns everything written to it —
// the plugin-side twin of the codebase's established captureStderr test pattern (R3: one copy per
// module, since no shared test-helper import crosses the module boundary).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	<-done
	return buf.String()
}

// TestInspectDefaultJSON_SnakeCaseCanonical is the GOLDEN test locking the DELIBERATE breaking output
// change: `charly box inspect <box>` (no --format) now marshals the spec.ResolvedBoxView as
// snake_case + omitempty JSON (json.MarshalIndent), NOT the former mixed-case json.Marshal(*ResolvedBox).
// The exact bytes below are the contract every downstream consumer parses.
func TestInspectDefaultJSON_SnakeCaseCanonical(t *testing.T) {
	view := spec.ResolvedBoxView{
		Name:                "fedora",
		Version:             "2026.194.0000",
		EffectiveVersion:    "2026.194.0000",
		Status:              "working",
		Base:                "quay.io/fedora/fedora:43",
		Platforms:           []string{"linux/amd64"},
		Tag:                 "v1",
		Registry:            "ghcr.io/opencharly",
		Pkg:                 "rpm",
		Distro:              []string{"fedora", "fedora-43"},
		BuildFormats:        []string{"rpm"},
		Candy:               []string{"ripgrep", "gh"},
		User:                "user",
		UID:                 1000,
		GID:                 1000,
		Home:                "/home/user",
		Builder:             map[string]string{"pixi": "fedora-builder"},
		BuilderCapabilities: []string{"pixi"},
		Network:             "charly",
		FullTag:             "ghcr.io/opencharly/charly-fedora:v1",
		Ports:               []string{"8080:8080"},
		Volumes:             []spec.ResolvedVolumeMount{{VolumeName: "charly-fedora-data", ContainerPath: "/home/user/data"}},
		Aliases:             []spec.CandyAlias{{Name: "rg", Command: "ripgrep"}},
		Engine:              "podman",
	}
	got, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	const want = `{
  "name": "fedora",
  "version": "2026.194.0000",
  "effective_version": "2026.194.0000",
  "status": "working",
  "base": "quay.io/fedora/fedora:43",
  "platforms": [
    "linux/amd64"
  ],
  "tag": "v1",
  "registry": "ghcr.io/opencharly",
  "pkg": "rpm",
  "distro": [
    "fedora",
    "fedora-43"
  ],
  "build_formats": [
    "rpm"
  ],
  "candy": [
    "ripgrep",
    "gh"
  ],
  "user": "user",
  "uid": 1000,
  "gid": 1000,
  "home": "/home/user",
  "builder": {
    "pixi": "fedora-builder"
  },
  "builder_capabilities": [
    "pixi"
  ],
  "network": "charly",
  "full_tag": "ghcr.io/opencharly/charly-fedora:v1",
  "ports": [
    "8080:8080"
  ],
  "volumes": [
    {
      "volume_name": "charly-fedora-data",
      "container_path": "/home/user/data"
    }
  ],
  "aliases": [
    {
      "name": "rg",
      "command": "ripgrep"
    }
  ],
  "engine": "podman"
}`
	if string(got) != want {
		t.Errorf("inspect default JSON drifted from the snake_case golden:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestInspectGrammar_Parse confirms `box inspect` accepts the box positional + --format / -i /
// --include-disabled flags (the surface formerly on the core InspectCmd).
func TestInspectGrammar_Parse(t *testing.T) {
	var g inspectGrammar
	if _, err := parseLeaf("inspect", &g, []string{"fedora", "--format", "ports", "-i", "dev", "--include-disabled"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.Box != "fedora" || g.Format != "ports" || g.Instance != "dev" || !g.IncludeDisabled {
		t.Errorf("parsed grammar = %+v, want fedora/ports/dev/true", g)
	}
}

// TestPrintInspectFormat_Coverage proves every documented --format field renders without erroring and
// an unknown one is rejected. (Output correctness for the scalar/aggregate fields is exercised end-to-
// end by the live R10 + the golden default-JSON test above.)
func TestPrintInspectFormat_Coverage(t *testing.T) {
	view := spec.ResolvedBoxView{
		FullTag: "t", Base: "b", Pkg: "p", Registry: "r", Network: "n", Version: "v", Info: "i",
		Builder:             map[string]string{"pixi": "fb"},
		BuilderCapabilities: []string{"pixi"},
		BuildFormats:        []string{"rpm"},
		Distro:              []string{"fedora"},
		Platforms:           []string{"linux/amd64"},
		Candy:               []string{"ripgrep"},
		Ports:               []string{"80:80"},
		Volumes:             []spec.ResolvedVolumeMount{{VolumeName: "vn", ContainerPath: "/p"}},
		Aliases:             []spec.CandyAlias{{Name: "a", Command: "c"}},
		// Engine intentionally empty → the "(global default)" fallback path.
	}
	for _, f := range []string{
		"tag", "base", "builder", "builds", "build", "distro", "pkg", "registry",
		"platforms", "candy", "network", "version", "status", "info",
		"ports", "volumes", "aliases", "engine",
	} {
		if err := printInspectFormat(view, f); err != nil {
			t.Errorf("printInspectFormat(%q) errored: %v", f, err)
		}
	}
	if err := printInspectFormat(view, "bogus"); err == nil {
		t.Error("printInspectFormat(bogus) must error")
	}
}

// TestResolveStatus mirrors the core resolveStatus contract re-implemented in the plugin.
func TestResolveStatus(t *testing.T) {
	if got := resolveStatus(""); got != "testing" {
		t.Errorf("resolveStatus(\"\") = %q, want testing", got)
	}
	if got := resolveStatus("working"); got != "working" {
		t.Errorf("resolveStatus(working) = %q, want working", got)
	}
}

// TestSortedKeys proves deterministic key ordering (the list/inspect output stability guarantee).
func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]int{"c": 1, "a": 1, "b": 1})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sortedKeys = %v, want [a b c]", got)
	}
}

// TestPrintImageTags_NoTagsErrors proves an empty inventory errors instead of silently printing
// nothing — mirroring the former charly/volume_cp_tags_cmd.go's ListTagsCmd.Run() contract.
func TestPrintImageTags_NoTagsErrors(t *testing.T) {
	if err := printImageTags(nil, ""); err == nil {
		t.Fatal("printImageTags(nil, \"\") must error, got nil")
	}
}

// TestPrintImageTags_BoxFilterNoMatchErrors proves a boxFilter that matches nothing errors with
// the box name named in the message (not a silent empty success).
func TestPrintImageTags_BoxFilterNoMatchErrors(t *testing.T) {
	tags := []spec.TagInfo{{Box: "fedora", Ref: "r1", Version: "v1"}}
	err := printImageTags(tags, "arch")
	if err == nil {
		t.Fatal("printImageTags with a non-matching boxFilter must error, got nil")
	}
	if !strings.Contains(err.Error(), "arch") {
		t.Errorf("error = %q, want it to name the unmatched box %q", err, "arch")
	}
}

// TestPrintImageTags_GroupsSortsAndFormats locks in the exact per-line output shape (byte-
// equivalent to the former ListTagsCmd.Run()): grouped by box (alphabetical), one line per tag
// tab-separated as box/ref/version, with a "(in use)" suffix only when InUse is set.
func TestPrintImageTags_GroupsSortsAndFormats(t *testing.T) {
	tags := []spec.TagInfo{
		{Box: "fedora", Ref: "fedora:v2", Version: "2026.200.0000", InUse: true},
		{Box: "arch", Ref: "arch:v1", Version: "2026.100.0000"},
		{Box: "fedora", Ref: "fedora:v1", Version: "2026.190.0000"},
	}
	out := captureStdout(t, func() {
		if err := printImageTags(tags, ""); err != nil {
			t.Errorf("printImageTags: %v", err)
		}
	})
	want := "arch\tarch:v1\t2026.100.0000\n" +
		"fedora\tfedora:v2\t2026.200.0000\t(in use)\n" +
		"fedora\tfedora:v1\t2026.190.0000\n"
	if out != want {
		t.Errorf("printImageTags output =\n%q\nwant\n%q", out, want)
	}
}

// TestPrintImageTags_BoxFilterNarrows proves a matching boxFilter excludes every other box's
// entries entirely.
func TestPrintImageTags_BoxFilterNarrows(t *testing.T) {
	tags := []spec.TagInfo{
		{Box: "fedora", Ref: "fedora:v1", Version: "2026.190.0000"},
		{Box: "arch", Ref: "arch:v1", Version: "2026.100.0000"},
	}
	out := captureStdout(t, func() {
		if err := printImageTags(tags, "arch"); err != nil {
			t.Errorf("printImageTags: %v", err)
		}
	})
	if strings.Contains(out, "fedora") {
		t.Errorf("printImageTags with boxFilter=arch leaked fedora's entry: %q", out)
	}
	if !strings.Contains(out, "arch:v1") {
		t.Errorf("printImageTags with boxFilter=arch missing arch's own entry: %q", out)
	}
}

// TestListServices_InitSystemsPredicate locks the listServices fix: the aggregate has_init
// bool is never populated by the resolution pipeline (PopulateCandyInitSystem fills the
// per-init-system init_systems map; the aggregate folds into HasContent, not HasInit), so
// the InitCandy predicate MUST read init_systems. A candy whose only trigger is the dead
// has_init field must NOT be listed — that is the regression this test guards.
func TestListServices_InitSystemsPredicate(t *testing.T) {
	rp := &spec.ResolvedProject{
		Candies: map[string]spec.CandyView{
			"with-init": {
				InitSystems: map[string]bool{"supervisord": true},
			},
			"with-relay": {
				PortRelayPorts: []int{18789},
			},
			"with-dead-has-init": {
				HasInit: true,
			},
			"plain": {},
		},
	}
	out := captureStdout(t, func() { listServices(rp) })
	for _, want := range []string{"with-init", "with-relay"} {
		if !strings.Contains(out, want) {
			t.Errorf("listServices missing %q (init_systems/port_relay trigger):\n%s", want, out)
		}
	}
	for _, notWant := range []string{"with-dead-has-init", "plain"} {
		if strings.Contains(out, notWant) {
			t.Errorf("listServices listed %q (no init_systems/port_relay trigger):\n%s", notWant, out)
		}
	}
}
