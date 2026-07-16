package main

import (
	"strings"
	"testing"
)

// The REVERSAL-PRESERVATION GATE for the package→plugin extraction: a
// run: {plugin: package} step is a TYPED-STEP verb (the SECOND, after service) —
// compileActOp MUST construct a *SystemPackagesStep (NOT a generic OpStep), so its
// Reverse() records the LOAD-BEARING ReverseOpPackageRemove (and ReverseOpCoprDisable for a
// copr repo). A shell-string OpStep (the path the RenderProvisionScript verbs take) would
// DROP them — that is exactly what this verb's TypedStepProvider exists to prevent. The
// keyword (run:) supplies the act intent the deleted Op.Do axis used to carry.
func TestCompileRunStep_PackagePluginLowersToSystemPackagesWithReversals(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "install redis", Op: Op{
		Plugin:      "package",
		PluginInput: map[string]any{"package": "redis"},
	}}}}
	steps := compileOpSteps(layer, testResolvedBox())

	var sp *SystemPackagesStep
	for _, s := range steps {
		if _, isOp := s.(*OpStep); isOp {
			t.Fatalf("plugin: package run step lowered to a generic OpStep — the load-bearing reversals would be DROPPED; got %#v", s)
		}
		if v, ok := s.(*SystemPackagesStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("plugin: package run step did not lower to a *SystemPackagesStep; got %#v", steps)
	}
	if len(sp.Packages) != 1 || sp.Packages[0] != "redis" {
		t.Fatalf("SystemPackagesStep.Packages = %v, want [redis]", sp.Packages)
	}
	// The install-phase step installs the package → Reverse() removes it.
	rev := sp.Reverse()
	if len(rev) != 1 || rev[0].Kind != ReverseOpPackageRemove {
		t.Errorf("plugin: package run step did not reverse to package-remove: %+v", rev)
	}
	// A PhasePrepare step carrying a copr repo reverses to ReverseOpCoprDisable — the OTHER
	// load-bearing reversal SystemPackagesStep records (and the typed step preserves), which
	// a generic OpStep cannot express.
	prep := &SystemPackagesStep{Format: "rpm", Phase: PhasePrepare, Copr: []string{"someuser/somerepo"}}
	var sawCopr bool
	for _, op := range prep.Reverse() {
		if op.Kind == ReverseOpCoprDisable {
			sawCopr = true
		}
	}
	if !sawCopr {
		t.Errorf("a copr PhasePrepare SystemPackagesStep did not reverse to copr-disable: %+v", prep.Reverse())
	}
}

// The package act renders into the box build: a build-context run: {plugin: package} step,
// emitted through the REAL box-build path (writeCandySteps→emitTasks), renders the
// dnf/apt/pacman install of the package as a Containerfile RUN via the provider's
// ProvisionActor (resolveProvisionScript: the ONE Op→act-shell seam). This proves the
// box-build `case "plugin"` seam handles the extracted package verb (not as an "unknown
// verb"), parallel to TestServicePluginActEmitsIntoBoxBuild for the typed pod-overlay path.
func TestPackagePluginActEmitsIntoBoxBuild(t *testing.T) {
	dir := t.TempDir()
	layer := &Candy{Name: "lyr"}
	g := &Generator{BuildDir: dir}
	op := Op{Plugin: "package", PluginInput: map[string]any{"package": "redis"}}
	var b strings.Builder
	if _, err := g.emitTasks(&b, layer, testResolvedBox(), []Op{op}, dir, ".build/test-img"); err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	for _, want := range []string{"RUN", "dnf install", "pacman -S", "redis"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitTasks Containerfile = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, `unknown verb "plugin"`) {
		t.Errorf("the raw plugin: package op was DROPPED as an unknown verb (the box-build regression):\n%s", out)
	}
}

// The REVERSAL-PRESERVATION GATE for the service→plugin extraction: a
// run: {plugin: service} step is the TYPED-STEP OUTLIER — compileActOp MUST construct a
// *ServicePackagedStep (NOT a generic OpStep), so its Reverse() records the LOAD-BEARING
// reversals (ReverseOpServiceDisable / RestoreEnabled / RemoveDropin). A shell-string
// OpStep (the path the other extracted state-provision verbs take) would DROP them — that
// is exactly what this verb's TypedStepProvider exists to prevent.
func TestCompileRunStep_ServicePluginLowersToServicePackagedWithReversals(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "enable sshd", Op: Op{
		Plugin:      "service",
		PluginInput: map[string]any{"service": "sshd"},
	}}}}
	steps := compileOpSteps(layer, testResolvedBox())

	var sp *ServicePackagedStep
	for _, s := range steps {
		if _, isOp := s.(*OpStep); isOp {
			t.Fatalf("plugin: service run step lowered to a generic OpStep — the load-bearing reversals would be DROPPED; got %#v", s)
		}
		if v, ok := s.(*ServicePackagedStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("plugin: service run step did not lower to a *ServicePackagedStep; got %#v", steps)
	}
	if sp.Unit != "sshd" || !sp.Enable {
		t.Fatalf("ServicePackagedStep = %+v, want Unit=sshd Enable=true", sp)
	}

	// The constructed step enables the unit → Reverse() disables it. Populate the
	// teardown-restore + drop-in fields the same way the use_packaged service path does,
	// and assert ALL THREE load-bearing reverse ops are produced — the property a generic
	// OpStep cannot express.
	sp.PriorEnabled = true
	sp.OverridesText = "[Service]\nEnvironment=X=1\n"
	sp.OverridesPath = "/etc/systemd/system/sshd.service.d/charly-x.conf"
	got := map[ReverseOpKind]bool{}
	for _, op := range sp.Reverse() {
		got[op.Kind] = true
	}
	for _, want := range []ReverseOpKind{ReverseOpServiceDisable, ReverseOpRestoreEnabled, ReverseOpRemoveDropin} {
		if !got[want] {
			t.Errorf("ServicePackagedStep.Reverse() missing %q; got %v", want, got)
		}
	}
}

// The service act renders into the box build: a build-context run: {plugin: service}
// step compiles to a ServicePackagedStep (via the TypedStepProvider) AND emits the enable
// directive into the OCI Containerfile — proving the full box-build path the extraction
// preserves end-to-end (compileActOp → ServicePackagedStep → candy/plugin-installstep
// service-packaged OpEmit, the C1.1-externalized build-emit).
func TestServicePluginActEmitsIntoBoxBuild(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "enable sshd", Op: Op{
		Plugin:      "service",
		PluginInput: map[string]any{"service": "sshd"},
		Context:     []string{"build"},
	}}}}
	steps := compileOpSteps(layer, testResolvedBox())
	tgt := ociTestTarget(buildEngineContext{})
	plan := &InstallPlan{Candy: "x", Steps: steps}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "enable packaged unit sshd") {
		t.Errorf("service act did not render the enable directive into the box build:\n%s", got)
	}
}

// TestCompileActOp_VerbWordWinsOverCollidingStepWord guards the C1.1 deploy regression: the word
// `file` is BOTH `verb:file` (candy/plugin-file, an in-proc ProvisionActor that drops a file) AND
// `step:file` (candy/plugin-installstep's build-emit-only class:step, C1.1). A `run: plugin: file`
// op (e.g. check-local-layer's marker drop) MUST lower to a generic OpStep — the verb act, whose
// deploy renders via resolveProvisionScript — NEVER to an externalStep (kind `external:file`), which
// the deploy walk would route to an OpExecute the build-emit-only step plugin cannot serve. Proves
// verb-first precedence in compileActOp.
func TestCompileActOp_VerbWordWinsOverCollidingStepWord(t *testing.T) {
	layer := &Candy{Name: "check-local-layer", plan: []Step{{Run: "drop the marker", Op: Op{
		Plugin:      "file",
		PluginInput: map[string]any{"file": "/etc/check-local-marker", "exists": true},
		Context:     []string{"deploy"},
	}}}}
	steps := compileOpSteps(layer, testResolvedBox())
	if len(steps) != 1 {
		t.Fatalf("want exactly 1 compiled step, got %d (%#v)", len(steps), steps)
	}
	if es, isExternal := steps[0].(*externalStep); isExternal {
		t.Fatalf("run: plugin: file lowered to an externalStep (kind %q) — verb-first precedence regressed; "+
			"the deploy walk would route it to OpExecute, which the build-emit-only step plugin cannot serve", es.Kind())
	}
	op, ok := steps[0].(*OpStep)
	if !ok {
		t.Fatalf("run: plugin: file must lower to an OpStep (the file verb act), got %T", steps[0])
	}
	if op.Op.Plugin != "file" {
		t.Fatalf("OpStep.Op.Plugin = %q, want %q", op.Op.Plugin, "file")
	}
}

// A build-context run: step folds into the install plan; a sibling check: step in
// the same plan does NOT (it is a runtime probe, not an install step).
func TestCompileOpSteps_FoldsBuildContextRunStepNotCheck(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{
		{Run: "install vim", Op: Op{Plugin: "package", PluginInput: map[string]any{"package": "vim"}, Context: []string{"build"}}},
		{Check: "vim present", Op: Op{Plugin: "file", PluginInput: map[string]any{"file": "/usr/bin/vim", "exists": true}}}, // a check: step → not folded
	}}
	steps := compileOpSteps(layer, testResolvedBox())
	pkgCount := 0
	for _, s := range steps {
		if _, ok := s.(*SystemPackagesStep); ok {
			pkgCount++
		}
	}
	if pkgCount != 1 {
		t.Fatalf("want exactly 1 folded package step (the check: probe excluded), got %d (%#v)", pkgCount, steps)
	}
}

// A runtime-only run: step (context: [runtime]) is NOT folded into the build
// plan — the check Runner executes it live, so folding would double-run.
func TestCompileOpSteps_DoesNotFoldRuntimeOnlyRunStep(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{
		{Run: "run", Op: Op{Plugin: "command", PluginInput: map[string]any{"command": "echo hi"}, Context: []string{"runtime"}}},
	}}
	if steps := compileOpSteps(layer, testResolvedBox()); len(steps) != 0 {
		t.Fatalf("runtime-only run: step must not be folded into the build plan, got %d steps", len(steps))
	}
}

// A run: command step (the install timeline; the former task: list) lowers to an
// OpStep — it must NOT be dropped, and the run-as user drives scope.
func TestCompileOpSteps_RunCommandLowersToOpStep(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "run cmd", Op: Op{Plugin: "command", PluginInput: map[string]any{"command": "echo hi"}, RunAs: "root"}}}}
	steps := compileOpSteps(layer, testResolvedBox())
	if len(steps) != 1 {
		t.Fatalf("run: command dropped: %d steps", len(steps))
	}
	op, ok := steps[0].(*OpStep)
	if !ok || op.Op.Plugin != "command" || op.Op.PluginInput["command"] != "echo hi" {
		t.Fatalf("run: command not compiled as an OpStep: %#v", steps[0])
	}
	// The run-as user (not the user verb) drives scope — root → system.
	if op.ResolvedUser != "root" && op.ResolvedUser != "0" {
		t.Errorf("OpStep.ResolvedUser = %q, want root (from RunAs)", op.ResolvedUser)
	}
}

// The validator's build-context "cannot act" rejection (a run: step whose verb, like `addr`, has no
// build/deploy install path) moved with the validateOps engine to candy/plugin-box (task #60); it is
// re-expressed as an on-disk fixture through the real `charly box validate` gate in
// validate_fixture_test.go (TestValidateOps_RejectsRuntimeOnlyActInBuild).
