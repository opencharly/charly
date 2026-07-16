package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// Tests for deploy.yml install_opts handling (Task 13).

func TestInstallOptsApplyTo(t *testing.T) {
	base := deploykit.EmitOpts{}
	o := &InstallOptsConfig{
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
	// InstallOptsConfig.ApplyTo. (False → false is a no-op; true
	// → true is also idempotent, so the only concern is never
	// clobbering a true with a false.)
	base := deploykit.EmitOpts{AllowRootTasks: true}
	o := &InstallOptsConfig{AllowRootTasks: false}
	got := deploykit.InstallOptsApplyTo(o, base)
	if !got.AllowRootTasks {
		t.Errorf("CLI-set AllowRootTasks was overwritten by zero deploy.yml value")
	}
}

func TestInstallOptsNilReceiver(t *testing.T) {
	var o *InstallOptsConfig
	base := deploykit.EmitOpts{Verify: true}
	got := deploykit.InstallOptsApplyTo(o, base)
	if got.Verify != true {
		t.Errorf("nil receiver modified opts: %+v", got)
	}
}

func TestInstallOptsBuilderImageMerge(t *testing.T) {
	// CLI override wins; deploy.yml fallback applies when CLI empty.
	cli := deploykit.EmitOpts{BuilderImageOverride: "cli-choice"}
	o := &InstallOptsConfig{BuilderImage: "yaml-choice"}
	got := deploykit.InstallOptsApplyTo(o, cli)
	if got.BuilderImageOverride != "cli-choice" {
		t.Errorf("CLI builder image was overwritten: %q", got.BuilderImageOverride)
	}

	noCli := deploykit.EmitOpts{}
	got = deploykit.InstallOptsApplyTo(o, noCli)
	if got.BuilderImageOverride != "yaml-choice" {
		t.Errorf("deploy.yml builder fallback not applied: %q", got.BuilderImageOverride)
	}
}
