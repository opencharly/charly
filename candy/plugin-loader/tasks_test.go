package loader

// Relocated from charly/tasks_test.go (#55 decoupling cone, Batch C):
// TestCandy_HasInstallFiles_IncludesTasks asserts loaderkit.CompleteCandyRunOps
// directly on a (Model, View) pair — zero charly coupling.

import (
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// TestCandy_HasInstallFiles_IncludesTasks proves the RunOps host-completion
// pass (loaderkit.CompleteCandyRunOps) folds a candy's `run:` steps into RunOps
// and OR-completes HasInstallFiles/HasContent with it.
func TestCandy_HasInstallFiles_IncludesTasks(t *testing.T) {
	m := spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "true"}}}}}
	v := spec.CandyView{}
	loaderkit.CompleteCandyRunOps(&m, &v)
	l := testCandy("x", m, v)
	if !l.HasTasks() {
		t.Fatal("HasTasks() should be true after loaderkit.CompleteCandyRunOps folds the run: step into RunOps")
	}
	if !l.HasInstallFiles() {
		t.Error("HasInstallFiles() should be true when HasTasks is true")
	}
}
