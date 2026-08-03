package main

import (
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/spec/spec"
)

// testResolvedBox returns a ResolvedBox suitable for feeding the
// task emitters. Uses fedora (rpm) by default with UID/GID 1000.
func testResolvedBox() *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "test-img", User: "user", UID: 1000, GID: 1000, Home: "/home/user", Pkg: "rpm", BuildFormats: []string{"rpm"}, Tags: []string{"all", "rpm"}}, DistroDef: testDistroDef("fedora")}
}

// --- Task.Kind() — exactly-one-verb enforcement ---

func TestTaskKind_Valid(t *testing.T) {
	cases := []struct {
		task spec.Op
		want string
	}{
		{cmdOp("echo hi"), "plugin"}, // command is a plugin verb now (plugin: command)
		{spec.Op{Mkdir: "/etc/foo"}, "mkdir"},
		{spec.Op{Copy: "foo", To: "/bar"}, "copy"},
		{spec.Op{Write: "/x", Content: "body"}, "write"},
		{spec.Op{Link: "/a", Target: "/b"}, "link"},
		{spec.Op{Download: "http://x"}, "download"},
		{spec.Op{Setcap: "/bin/x"}, "setcap"},
		{spec.Op{Build: "all"}, "build"},
	}
	for _, c := range cases {
		got, err := c.task.Kind()
		if err != nil {
			t.Errorf("Kind(%+v) error: %v", c.task, err)
		}
		if got != c.want {
			t.Errorf("Kind(%+v) = %q, want %q", c.task, got, c.want)
		}
	}
}

// Zero-verb and multiple-verb enforcement on the unified Op.Kind() is covered
// by TestCheck_Kind in checkspec_test.go (one Kind() implementation, one set of
// tests — R3). TestTaskKind_Valid above covers the install-verb names that
// TestCheck_Kind's probe-verb cases do not.
//
// The pure var-substitution / user-resolution / inline-staging / per-verb
// Containerfile-line emitter tests (TaskSubstAutoExports, TaskSubstPath,
// TaskUnresolvedRefs, ResolveUserSpec, StageInlineContent, EmitMkdirBatch,
// EmitCopy, EmitWrite, EmitLinkBatch, EmitSetcapBatch, EmitDownload,
// TaskCacheMounts, EmitCmd, EmitVarsEnv) moved to
// sdk/deploykit/tasks_emit_test.go alongside the K3 alias dissolution — they
// exercise deploykit.* directly now, no core Generator/Config needed. The
// EmitTasks ORCHESTRATOR tests (TestEmitTasks_UserCoalescing / _CommandEmitsRun /
// _UserSwitches / _OrderPreserved / _ParentDirAutoInsert / _ParentDirSuppressedWhenDeclared
// / _WriteStagesContent) + the plugin-verb DISPATCH tests moved there too in
// #55 cone-render Unit A (the production-dead charly toDeploykit() wrapper was deleted).

// --- Validator ---
//
// The five former validateCandyTasks host tests (CopyRequiresTo, UnresolvedVar, ReservedVarKey,
// BuildOnlyAll, HappyPath) moved with the validateCandyTasks rule to candy/plugin-box (task #60).
// They are re-expressed as on-disk fixtures driven through the real `charly box validate` gate in
// validate_fixture_test.go (TestValidate_Task*). The bad-mode (octal ^0[0-7]{3,4}$) rejection is a
// CUE concern (#Op.mode) — see cue_tighten_test.go "candy run step bad mode rejected".

// --- Parity: ensure HasInstallFiles picks up HasTasks ---

// TestCandy_HasInstallFiles_IncludesTasks relocated to
// candy/plugin-loader/tasks_test.go (#55 decoupling cone, Batch C) — it
// asserted loaderkit.CompleteCandyRunOps directly, zero charly coupling.
