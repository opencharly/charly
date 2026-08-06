package fleet

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// install_opts_test.go — relocated from charly/install_opts_test.go (#55 decoupling, Batch A):
// all 4 tests assert deploykit.InstallOptsApplyTo directly, zero charly coupling.

func TestInstallOptsApplyTo(t *testing.T) {
	base := spec.EmitOpts{}
	o := &vmshared.InstallOptsConfig{
		WithServices:     true,
		AllowRepoChanges: true,
		Verify:           true,
		BuilderImage:     "fedora-builder:2026.04",
	}
	got := deploykit.InstallOptsApplyTo(o, base)
	if !got.WithServices {
		t.Errorf("WithServices not applied")
	}
	if !got.AllowRepoChanges {
		t.Errorf("AllowRepoChanges not applied")
	}
	if !got.Verify {
		t.Errorf("Verify not applied")
	}
	if got.BuilderImageOverride != "fedora-builder:2026.04" {
		t.Errorf("BuilderImageOverride = %q", got.BuilderImageOverride)
	}
}

func TestInstallOptsCLIOverridesWin(t *testing.T) {
	// CLI sets AllowRootTasks via --allow-root-tasks; deploy.yml
	// doesn't. The CLI value must not be reset to false by
	// vmshared.InstallOptsConfig.ApplyTo. (False → false is a no-op; true
	// → true is also idempotent, so the only concern is never
	// clobbering a true with a false.)
	base := spec.EmitOpts{AllowRootTasks: true}
	o := &vmshared.InstallOptsConfig{AllowRootTasks: false}
	got := deploykit.InstallOptsApplyTo(o, base)
	if !got.AllowRootTasks {
		t.Errorf("CLI-set AllowRootTasks was overwritten by zero deploy.yml value")
	}
}

func TestInstallOptsNilReceiver(t *testing.T) {
	var o *vmshared.InstallOptsConfig
	base := spec.EmitOpts{Verify: true}
	got := deploykit.InstallOptsApplyTo(o, base)
	if got.Verify != true {
		t.Errorf("nil receiver modified opts: %+v", got)
	}
}

func TestInstallOptsBuilderImageMerge(t *testing.T) {
	// CLI override wins; deploy.yml fallback applies when CLI empty.
	cli := spec.EmitOpts{BuilderImageOverride: "cli-choice"}
	o := &vmshared.InstallOptsConfig{BuilderImage: "yaml-choice"}
	got := deploykit.InstallOptsApplyTo(o, cli)
	if got.BuilderImageOverride != "cli-choice" {
		t.Errorf("CLI builder image was overwritten: %q", got.BuilderImageOverride)
	}

	noCli := spec.EmitOpts{}
	got = deploykit.InstallOptsApplyTo(o, noCli)
	if got.BuilderImageOverride != "yaml-choice" {
		t.Errorf("deploy.yml builder fallback not applied: %q", got.BuilderImageOverride)
	}
}
