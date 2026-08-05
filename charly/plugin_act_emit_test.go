package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The act-emit enabler renders a run: step whose verb is a state-provision plugin
// (plugin: <verb> + plugin_input, the provider implementing ProvisionActor) into shell at
// install emit — the gap that opened once unix_group left #Op. Both install-emit paths
// (the reverse-channel RunHostStep for the local/vm/pod deploy targets — every deploy
// target is out-of-process now, driven through kit.WalkPlans — and the OCI pod-overlay Op
// build-emit via the step:op ops.OpEmit → step-emit seam → emitTasks `case "plugin"`) reach
// the provider's RenderProvisionScript via the shared resolveProvisionScript seam (R3).
//
// testRenderOpCommand replicates the former renderOpCommand wrapper's exact behavior
// (dead-code-radical-removal-batch deletion — its "in-proc deploy path" caller class no
// longer exists, since every deploy target is out-of-process; RunHostStep reaches this
// identical resolveProvisionScript seam live in production) — kept test-local since the
// tests below still want to exercise the SAME structured-verb-first, act-plugin-fallback
// resolution renderOpCommand did, not just resolveProvisionScript in isolation. This
// function is SHARED with heredoc_balance_test.go (the write: heredoc case) — grep-confirmed
// the only two op shapes exercised package-wide are op.Write and a state-provision op.Plugin;
// sdk/kit.RenderOpCommand's OTHER cases (op.Command/Mkdir/Link/Setcap/Download/"plugin:command")
// are NOT ported here and would need porting from sdk/kit/render.go if a future fixture needs
// them — this is a deliberate PARTIAL local port, not a claim that it covers RenderOpCommand's
// full switch.
func testRenderOpCommand(s *spec.OpStep) (string, error) {
	if s.Op == nil {
		return "", fmt.Errorf("testRenderOpCommand: nil op")
	}
	op := s.Op
	switch {
	case op.Copy != "":
		return "", fmt.Errorf("copy: task must be staged via PutFile, not rendered")
	case op.Write != "":
		// Mirrors sdk/kit.RenderOpCommand's op.Write case exactly (render.go) — the ONE
		// non-plugin verb shape a test in this package exercises (heredoc_balance_test.go's
		// TestRenderTaskCommand_WriteHeredocBalanced).
		mode := op.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s /dev/stdin %s <<'CHARLY_WRITE'\n%s\nCHARLY_WRITE",
			mode, testShDoubleQuote(op.Write), op.Content), nil
	case op.Plugin != "" && op.Plugin != "command":
		// A state-provision act-plugin verb — resolveProvisionScript is the REAL production
		// seam (package main), exercising the actual registered ProvisionActor.
		script, ok := resolveProvisionScript(op, s.Distros)
		if !ok {
			return "", fmt.Errorf("run: plugin verb %q is not act-capable (no ProvisionActor)", op.Plugin)
		}
		return script, nil
	}
	return "", fmt.Errorf("testRenderOpCommand: unsupported op shape %+v (only write: and a state-provision plugin: verb are ported here — see sdk/kit/render.go for the full RenderOpCommand switch)", op)
}

// testShDoubleQuote mirrors sdk/kit.ShDoubleQuote (render.go) exactly — a small, pure,
// stdlib-only string escape with zero engine coupling.
func testShDoubleQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "`", "\\`")
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// unixGroupActStep is the canonical exercise op: a `run:` step authoring the extracted
// unix_group verb as a plugin (groupadd checkgrp with gid 4242).
func unixGroupActStep() *spec.OpStep {
	gid := 4242
	return &spec.OpStep{
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
	s := &spec.OpStep{Op: &spec.Op{Plugin: "process", PluginInput: map[string]any{"process": "bash"}}}
	if _, err := testRenderOpCommand(s); err == nil {
		t.Fatalf("testRenderOpCommand(plugin: process) err=nil, want a not-act-capable error")
	}
}

// rawUnixGroupOp is the RAW plan op the box build walks straight into emitTasks —
// Plugin set, NO pre-conversion (the actual shipping shape, not an OpStep wrapper).
func rawUnixGroupOp() spec.Op {
	return spec.Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "checkgrp", "gid": 4242}}
}

// The extracted state-provision verbs (user / kernel-param / mount) reach the SAME
// resolveProvisionScript seam through renderOpCommand — each renders its act shell from
// plugin_input via its provider's ProvisionActor. One renderOpCommand assertion per verb
// proves the act half emits at the local/vm deploy seam. The box-build emitTasks seam is
// verb-agnostic (it dispatches every non-command plugin verb through the EmitPluginOp seam);
// that dispatch is proven generically in sdk/deploykit (TestEmitTasks_PluginVerb_DispatchesToSeam_Verbatim
// + _ActScriptWrappedInRun) — the former charly toDeploykit()-based box-build assertions were
// relocated there in #55 cone-render Unit A (the charly EmitPluginOp bridge was production-dead).

// renderOpCommand turns a plugin: user run-Op into the idempotent useradd shell.
func TestRenderOpCommand_PluginAct_User(t *testing.T) {
	s := &spec.OpStep{
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
	s := &spec.OpStep{
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
	s := &spec.OpStep{
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

// The OCI pod-overlay OpStep build-emit (C1.5) routes the RAW plugin act op through the FULL
// step-emit chain: OpStep → deploykit.OCITarget.Emit → ociEmitStep → dispatchOCIStep →
// candy/plugin-installstep's "oci-dispatch" → pluginEmitStepWords[Op]="op" →
// InvokeProvider("step","op") → candy/plugin-installstep ops.OpEmit → the plugin's OWN
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
	// The op is a `run: plugin: unix_group` act. Its render used to also need the render-seam's
	// own per-dir Generator cache seeded; that seam (and the cache) are gone — EmitPluginOp went
	// plugin-side in P8b and the last two methods in K-wave 2 cone R1 — so the resolved-project
	// stub above is now the whole fixture.
	tgt := ociTestTarget(buildEngineContext{Box: &box.ResolvedBox, ImageBuildDir: cwd, ContextRelPrefix: ".build/test-img"})
	op := rawUnixGroupOp()
	plan := &spec.InstallPlan{Candy: "lyr", Steps: []spec.InstallStep{&spec.OpStep{Op: &op, CandyName: "lyr"}}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := tgt.String()
	for _, want := range []string{"RUN", "groupadd", "checkgrp", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("OpStep build-emit Containerfile = %q, want substring %q", out, want)
		}
	}
}
