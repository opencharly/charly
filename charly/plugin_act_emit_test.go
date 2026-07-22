package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// The act-emit enabler renders a run: step whose verb is a state-provision plugin
// (plugin: <verb> + plugin_input, the provider implementing ProvisionActor) into shell at
// install emit — the gap that opened once unix_group left #Op. Both install-emit paths
// (the reverse-channel RunHostStep for the local/vm/pod deploy targets — every deploy
// target is out-of-process now, driven through kit.WalkPlans — and the OCI pod-overlay Op
// build-emit via the step:op OpEmit → step-emit seam → emitTasks `case "plugin"`) reach
// the provider's RenderProvisionScript via the shared resolveProvisionScript seam (R3).
//
// testRenderOpCommand replicates the former renderOpCommand wrapper's exact behavior
// (dead-code-radical-removal-batch deletion — its "in-proc deploy path" caller class no
// longer exists, since every deploy target is out-of-process; RunHostStep reaches this
// identical resolveProvisionScript seam live in production) — kept test-local since the
// tests below still want to exercise the SAME structured-verb-first, act-plugin-fallback
// resolution renderOpCommand did, not just resolveProvisionScript in isolation.
func testRenderOpCommand(s *deploykit.OpStep) (string, error) {
	if s.Op == nil {
		return "", fmt.Errorf("testRenderOpCommand: nil op")
	}
	if s.Op.Copy != "" {
		return "", fmt.Errorf("copy: task must be staged via PutFile, not rendered")
	}
	if cmd, handled := kit.RenderOpCommand(s.Op, s.CtxPath, s.CandyVars); handled {
		return cmd, nil
	}
	script, ok := resolveProvisionScript(s.Op, s.Distros)
	if !ok {
		return "", fmt.Errorf("run: plugin verb %q is not act-capable (no ProvisionActor)", s.Op.Plugin)
	}
	return script, nil
}

// unixGroupActStep is the canonical exercise op: a `run:` step authoring the extracted
// unix_group verb as a plugin (groupadd checkgrp with gid 4242).
func unixGroupActStep() *deploykit.OpStep {
	gid := 4242
	return &deploykit.OpStep{
		Op:        &spec.Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "checkgrp", "gid": gid}},
		CandyName: "lyr",
	}
}

// renderOpCommand (the local/vm deploy emit) turns a plugin: unix_group run-Op into the
// idempotent groupadd shell.
func TestRenderOpCommand_PluginAct_UnixGroup(t *testing.T) {
	cmd, err := testRenderOpCommand(unixGroupActStep())
	if err != nil {
		t.Fatalf("renderOpCommand: %v", err)
	}
	for _, want := range []string{"groupadd", "-g 4242", "checkgrp"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("renderOpCommand = %q, want substring %q", cmd, want)
		}
	}
}

// A run: step whose plugin verb is NOT a ProvisionActor (an observe-only verb) has no
// build/deploy install path — renderOpCommand errors loudly rather than silently dropping
// the step (R4: no silent drop).
func TestRenderOpCommand_PluginAct_NotActCapable(t *testing.T) {
	s := &deploykit.OpStep{Op: &spec.Op{Plugin: "process", PluginInput: map[string]any{"process": "bash"}}}
	if _, err := testRenderOpCommand(s); err == nil {
		t.Fatalf("testRenderOpCommand(plugin: process) err=nil, want a not-act-capable error")
	}
}

// rawUnixGroupOp is the RAW plan op the box build walks straight into emitTasks —
// Plugin set, NO pre-conversion (the actual shipping shape, not an OpStep wrapper).
func rawUnixGroupOp() spec.Op {
	return spec.Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "checkgrp", "gid": 4242}}
}

// emitTasks IS the real box-build emit path (writeCandySteps → g.emitTasks walks the
// candy's runOps straight here). It must render a RAW plugin: unix_group run-Op — with NO
// pre-conversion — into a Containerfile RUN carrying the groupadd. This guards the box-build
// `case "plugin"` seam DIRECTLY: the box build never goes through the pod-overlay OpStep
// build-emit, so a missing `case "plugin"` in emitTasks would silently drop the groupadd as
// `# unknown verb "plugin"` even if the overlay path stayed green.
func TestEmitTasks_PluginAct_UnixGroup(t *testing.T) {
	dir := t.TempDir()
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	g := &Generator{BuildDir: dir}
	var b strings.Builder
	if _, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), []spec.Op{rawUnixGroupOp()}, dir, ".build/test-img"); err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	for _, want := range []string{"RUN", "groupadd", "checkgrp", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitTasks Containerfile = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, `unknown verb "plugin"`) {
		t.Errorf("the raw plugin op was DROPPED as an unknown verb (the box-build regression):\n%s", out)
	}
}

// rawFileRunOp is the RAW plan op the box build walks straight into emitTasks for a
// run: file step — Plugin set, file/mode in plugin_input, content a SHARED #Op modifier.
func rawFileRunOp() spec.Op {
	return spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/app/seed.conf", "mode": "0600"}, Content: "hello"}
}

// TestEmitTasks_PluginAct_File is the main-repo equivalent of the box/fedora check-pod
// generate `grep -c 'unknown verb' Containerfile == 0`: it runs the REAL box-build emit
// path (g.emitTasks) on a raw plugin: file run-Op and proves it renders the RUNTIME
// file-creation (mkdir/cat+chmod) into a Containerfile RUN — NOT dropped as
// `# unknown verb "plugin"`. file's act reaches the SAME resolveProvisionScript seam as
// unix_group, so this guards the file ProvisionActor wiring end-to-end through the
// install-emit pipeline.
func TestEmitTasks_PluginAct_File(t *testing.T) {
	dir := t.TempDir()
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	g := &Generator{BuildDir: dir}
	var b strings.Builder
	if _, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), []spec.Op{rawFileRunOp()}, dir, ".build/test-img"); err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	for _, want := range []string{"RUN", "mkdir", "/etc/app/seed.conf", "chmod", "0600"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitTasks Containerfile = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, "unknown verb") {
		t.Errorf("the raw plugin: file op was DROPPED as an unknown verb (the box-build regression):\n%s", out)
	}
}

// The other extracted state-provision verbs (user / kernel-param / mount) reach the SAME
// resolveProvisionScript seam through renderOpCommand — each renders its act shell from
// plugin_input via its provider's ProvisionActor. One renderOpCommand assertion per verb
// proves the act half emits at the local/vm deploy seam; the box-build emitTasks seam is
// verb-agnostic (it calls resolveProvisionScript too — proven generic by
// TestEmitTasks_PluginAct_UnixGroup, TestEmitTasks_PluginAct_File and
// TestEmitTasks_PluginAct_KernelParam below).

// renderOpCommand turns a plugin: user run-Op into the idempotent useradd shell.
func TestRenderOpCommand_PluginAct_User(t *testing.T) {
	s := &deploykit.OpStep{
		Op:        &spec.Op{Plugin: "user", PluginInput: map[string]any{"user": "svc", "uid": 1500, "home": "/home/svc"}},
		CandyName: "lyr",
	}
	cmd, err := testRenderOpCommand(s)
	if err != nil {
		t.Fatalf("renderOpCommand: %v", err)
	}
	for _, want := range []string{"useradd", "-u 1500", "svc", "-m -d '/home/svc'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("renderOpCommand = %q, want substring %q", cmd, want)
		}
	}
}

// renderOpCommand turns a plugin: mount run-Op into the idempotent mount shell.
func TestRenderOpCommand_PluginAct_Mount(t *testing.T) {
	s := &deploykit.OpStep{
		Op:        &spec.Op{Plugin: "mount", PluginInput: map[string]any{"mount": "/mnt/data", "mount_source": "/dev/sdb1", "filesystem": "ext4"}},
		CandyName: "lyr",
	}
	cmd, err := testRenderOpCommand(s)
	if err != nil {
		t.Fatalf("renderOpCommand: %v", err)
	}
	for _, want := range []string{"findmnt", "mount", "-t 'ext4'", "'/dev/sdb1'", "'/mnt/data'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("renderOpCommand = %q, want substring %q", cmd, want)
		}
	}
}

// renderOpCommand turns a plugin: kernel-param run-Op into the sysctl -w shell. The `value`
// matcher rides plugin_input and is read via the kernel-param candy's matcher codec
// (candy/plugin-kernel-param, resolved through the registry as a kit.ProvisionActor).
func TestRenderOpCommand_PluginAct_KernelParam(t *testing.T) {
	s := &deploykit.OpStep{
		Op:        &spec.Op{Plugin: "kernel-param", PluginInput: map[string]any{"kernel-param": "vm.swappiness", "value": "10"}},
		CandyName: "lyr",
	}
	cmd, err := testRenderOpCommand(s)
	if err != nil {
		t.Fatalf("renderOpCommand: %v", err)
	}
	for _, want := range []string{"sysctl -w", "vm.swappiness", "10"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("renderOpCommand = %q, want substring %q", cmd, want)
		}
	}
}

// rawKernelParamOp is the RAW plan op the box build walks straight into emitTasks.
func rawKernelParamOp() spec.Op {
	return spec.Op{Plugin: "kernel-param", PluginInput: map[string]any{"kernel-param": "vm.swappiness", "value": "10"}}
}

// emitTasks (the REAL box-build emit path) must render a RAW plugin: kernel-param run-Op
// into a Containerfile RUN carrying the sysctl write — proving the box-build `case "plugin"`
// seam is verb-agnostic across the extracted state-provision verbs (not unix_group-special).
func TestEmitTasks_PluginAct_KernelParam(t *testing.T) {
	dir := t.TempDir()
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	g := &Generator{BuildDir: dir}
	var b strings.Builder
	if _, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), []spec.Op{rawKernelParamOp()}, dir, ".build/test-img"); err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	for _, want := range []string{"RUN", "sysctl -w", "vm.swappiness", "10"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitTasks Containerfile = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, `unknown verb "plugin"`) {
		t.Errorf("the raw plugin op was DROPPED as an unknown verb:\n%s", out)
	}
}

// The OCI pod-overlay OpStep build-emit (C1.5) routes the RAW plugin act op through the FULL
// step-emit chain: OpStep → deploykit.OCITarget.Emit → ociEmitStep → pluginEmitStepWords[Op]="op" →
// ociSpliceClassStepEmit("op") → candy/plugin-installstep OpEmit → the plugin's OWN
// "resolved-project"-built deploykit.Generator → dg.EmitTasks `case "plugin"`. This proves the
// pod-overlay build and the box build still share the ONE `case "plugin"` seam (no pre-conversion)
// even after the OpStep build-emit's HOST-COUPLED render moved off the step-emit host-builder.
func TestEmitOp_PluginAct_UnixGroup_OCI(t *testing.T) {
	// testResolvedBox() reads fixtures relative to the package's testdata dir — capture it BEFORE
	// chdirTemp changes the process cwd for the plugin's resolved-project cache-key isolation.
	box := testResolvedBox()
	cwd := chdirTemp(t)
	stubResolvedProject(t, spec.ResolvedProject{
		Boxes:       map[string]spec.ResolvedBoxView{"test-img": {Name: "test-img", UID: 1000, GID: 1000, Home: "/home/user", User: "user"}},
		CandyModels: map[string]spec.CandyModel{"lyr": {Name: "lyr"}},
		Candies:     map[string]spec.CandyView{"lyr": {}},
	})
	// The op is a `run: plugin: unix_group` act — its render reaches EmitPluginOp, the ONE
	// render-seam that stays host-coupled (a Go-level ProvisionActor type-assertion), so it needs
	// renderGenCache seeded too (a SEPARATE cache from the resolved-project stub above).
	stubRenderGen(t, cwd, box)
	tgt := ociTestTarget(buildEngineContext{Box: box, ImageBuildDir: cwd, ContextRelPrefix: ".build/test-img"})
	op := rawUnixGroupOp()
	plan := &deploykit.InstallPlan{Candy: "lyr", Steps: []spec.InstallStep{&deploykit.OpStep{Op: &op, CandyName: "lyr"}}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := tgt.String()
	for _, want := range []string{"RUN", "groupadd", "checkgrp", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("OpStep build-emit Containerfile = %q, want substring %q", out, want)
		}
	}
}
