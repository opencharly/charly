package main

// plugin_connected_set_test.go — the CONNECTED PLUGIN SET the loader records when it serves a
// unit (opencharly/charly#1131). The properties under test are the ones a consumer depends on:
// the set names the binary the connect ACTUALLY served, it is keyed so a step can select an entry
// by leaf, a re-load of the same bytes is not a second plugin, and a caller can never mutate the
// registry through the copy it was handed.

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestConnectedPluginSetRecordsWhatTheLoaderServed(t *testing.T) {
	connectedPluginSet.forget()
	t.Cleanup(connectedPluginSet.forget)

	recordConnectedPluginFromLoad(
		"github.com/opencharly/plugin-deploy-firecracker/candy/plugin-deploy-firecracker",
		"/home/op/.cache/charly/plugin-build/views/plugin-deploy-firecracker-4acc8316daba136b",
		"/src/plugin-deploy-firecracker/candy/plugin-deploy-firecracker",
	)
	recordConnectedPluginFromLoad(
		"plugin-firecracker",
		"/home/op/.cache/charly/plugin-build/views/plugin-firecracker-0123456789abcdef",
		"",
	)
	// A re-load of the SAME served bytes is not a second plugin.
	recordConnectedPluginFromLoad(
		"plugin-firecracker",
		"/home/op/.cache/charly/plugin-build/views/plugin-firecracker-0123456789abcdef",
		"",
	)

	got := connectedPluginSet.snapshot()
	if len(got) != 2 {
		t.Fatalf("snapshot() = %d entries, want 2 (the identical second load must not duplicate): %+v", len(got), got)
	}
	first := got[0]
	if first.Key != "github.com/opencharly/plugin-deploy-firecracker/candy/plugin-deploy-firecracker" {
		t.Errorf("Key = %q, want the scanned candy key verbatim", first.Key)
	}
	// The leaf is bakedPluginFileName's rule — the FILENAME the loader opens a delivered binary by
	// — never a second derivation of it.
	if want := bakedPluginFileName(first.Key); first.Leaf != want || first.Leaf != "plugin-deploy-firecracker" {
		t.Errorf("Leaf = %q, want %q (the delivered binary's filename)", first.Leaf, want)
	}
	if first.ViewPath != "/home/op/.cache/charly/plugin-build/views/plugin-deploy-firecracker-4acc8316daba136b" {
		t.Errorf("ViewPath = %q, want the served binary", first.ViewPath)
	}
	if got[1].SrcDir != "" {
		t.Errorf("a source-less served plugin must keep SrcDir empty, got %q", got[1].SrcDir)
	}

	// A caller holds a COPY: mutating it cannot change what the next reader sees.
	got[0].ViewPath = "/mutated"
	if again := connectedPluginSet.snapshot(); again[0].ViewPath == "/mutated" {
		t.Fatal("snapshot() handed out the registry's own storage: a caller mutated the registry")
	}
}

func TestConnectedPluginSetIgnoresAnEntryWithNoServedBinary(t *testing.T) {
	connectedPluginSet.forget()
	t.Cleanup(connectedPluginSet.forget)

	connectedPluginSet.recordOne(spec.DeployConnectedPlugin{Key: "plugin-x", Leaf: "plugin-x"})
	if got := connectedPluginSet.snapshot(); got != nil {
		t.Fatalf("an entry with no served binary was recorded: %+v", got)
	}
}
