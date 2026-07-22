package bundle

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// recordingExec is a DeployExecutor that records PutFile/RunUser calls and returns configurable
// values for RunCapture/ResolveHome — ported from charly/deploy_home_test.go (S3b: the functions
// it exercised, recordVenueLedger + prepareReverseState, moved here).
type recordingExec struct {
	homeReturn       string
	homeErr          error
	resolveHomeCalls int
	runCaptureReturn string
	userScripts      []string
}

func (e *recordingExec) Venue() string                                               { return "rec://test" }
func (e *recordingExec) RunSystem(context.Context, string, deploykit.EmitOpts) error { return nil }
func (e *recordingExec) RunUser(_ context.Context, script string, _ deploykit.EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *recordingExec) RunBuilder(context.Context, deploykit.BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExec) PutFile(context.Context, string, string, uint32, bool, deploykit.EmitOpts) error {
	return nil
}
func (e *recordingExec) GetFile(context.Context, string, bool, deploykit.EmitOpts) ([]byte, error) {
	return nil, os.ErrNotExist
}
func (e *recordingExec) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *recordingExec) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *recordingExec) RunCapture(context.Context, string) (string, string, int, error) {
	return e.runCaptureReturn, "", 0, nil
}
func (e *recordingExec) Kind() string { return "rec" }
func (e *recordingExec) ResolveHome(context.Context, string) (string, error) {
	e.resolveHomeCalls++
	if e.homeErr != nil {
		return "", e.homeErr
	}
	return e.homeReturn, nil
}

// TestExternalDeployRecordVenueLedger_RemoteWritesGuestLedger proves the externalized vm deploy
// writes the SELF-CONTAINED ledger INTO THE VENUE (the guest) over the executor — the deploy +
// per-candy layer records + both ledger dirs — restoring what the in-proc VmDeployTarget wrote via
// *Via(t.Exec) and what the bed's guest-ledger probes (ah-deploy-recorded / ah-ledger-deploys-dir /
// ah-ledger-layers-dir) assert.
func TestExternalDeployRecordVenueLedger_RemoteWritesGuestLedger(t *testing.T) {
	fe := &recordingExec{} // a non-ShellExecutor venue → the remote (guest) write path
	plans := []*deploykit.InstallPlan{{Candy: "ripgrep", Version: "2026.1.1", DeployID: "abc1230000000000"}}
	if err := recordVenueLedger(fe, plans, "check-arch-vm", "vm", t.TempDir()); err != nil {
		t.Fatalf("recordVenueLedger: %v", err)
	}
	all := strings.Join(fe.userScripts, "\n")
	if !strings.Contains(all, "installed/layers/ripgrep.json") {
		t.Errorf("guest layer record not written via the executor:\n%s", all)
	}
	if !strings.Contains(all, "installed/deploys/abc1230000000000.json") {
		t.Errorf("guest deploy record not written via the executor:\n%s", all)
	}
	if !strings.Contains(all, "installed/layers") || !strings.Contains(all, "installed/deploys") || !strings.Contains(all, "mkdir -p") {
		t.Errorf("guest ledger dirs not created via the executor:\n%s", all)
	}
	// R10 bed-found bug #6 (2026.203.0135): the ported recordVenueLedger dropped the word param
	// entirely, leaving DeployRecord.Target == "" — which the egress schema's `target: !=""`
	// constraint rejects at write time in a real guest deploy. Assert the word survives into the
	// written record's JSON verbatim; this assertion FAILS on the pre-fix signature (Target unset).
	if !strings.Contains(all, `"target": "vm"`) {
		t.Errorf("deploy record's target field did not carry the deploy word through to the venue write:\n%s", all)
	}
}

// TestExternalDeployRecordVenueLedger_HostLocalIsNoop proves a HOST-LOCAL venue (ShellExecutor)
// skips the venue write — recordDeploy already wrote the operator-side ledger there, so the venue
// IS the host and a second write would be redundant.
func TestExternalDeployRecordVenueLedger_HostLocalIsNoop(t *testing.T) {
	plans := []*deploykit.InstallPlan{{Candy: "direnv", DeployID: "deadbeef00000000"}}
	if err := recordVenueLedger(kit.ShellExecutor{}, plans, "host-bed", "local", t.TempDir()); err != nil {
		t.Fatalf("recordVenueLedger host-local: %v", err)
	}
}

// TestPrepareReverseState_SkipsVenueExecForApkOnlyPlan proves a plan carrying only an
// ApkInstallStep (an android device deploy) or otherwise no home-token / ServicePackaged step must
// NOT pay a live venue ResolveHome exec — that unconditional exec hard-failed (exit 125 "no such
// container" / exit 255 ssh) when the venue wasn't reachable yet. The guard skips it; a home-token
// plan still resolves.
func TestPrepareReverseState_SkipsVenueExecForApkOnlyPlan(t *testing.T) {
	// apk-only plan → guard skips ResolveHome entirely → no error even though the venue exec would fail.
	apkExec := &recordingExec{homeErr: context.DeadlineExceeded}
	apkPlan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ApkInstallStep{Packages: []deploykit.ApkPackageSpec{{Package: "com.example"}}, CandyName: "app"},
	}}
	if err := prepareReverseState(context.Background(), apkExec, []*deploykit.InstallPlan{apkPlan}); err != nil {
		t.Fatalf("apk-only plan: prepareReverseState should skip the venue exec, got err: %v", err)
	}
	if apkExec.resolveHomeCalls != 0 {
		t.Errorf("apk-only plan called ResolveHome %d times, want 0 (guard must skip the unnecessary venue exec)", apkExec.resolveHomeCalls)
	}

	// A home-token plan (ShellHookStep) DOES need the home → ResolveHome is called, and its
	// failure surfaces (proving the guard didn't over-skip).
	homeExec := &recordingExec{homeErr: context.DeadlineExceeded}
	homePlan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ShellHookStep{EnvVars: map[string]string{"P": "{{.Home}}/.npm"}, CandyName: "nodejs"},
	}}
	if err := prepareReverseState(context.Background(), homeExec, []*deploykit.InstallPlan{homePlan}); err == nil {
		t.Error("home-token plan: prepareReverseState should surface the ResolveHome failure, got nil")
	}
	if homeExec.resolveHomeCalls != 1 {
		t.Errorf("home-token plan called ResolveHome %d times, want 1", homeExec.resolveHomeCalls)
	}
}

// TestExternalDeploy_FillsPackageRemoveUninstallCmdOnRecord proves the C19 latent-drop fix (ported
// to its new home, S3b): recordDeploy fills a package-remove ReverseOp's UninstallCmd from the
// marshalled DistroConfig BEFORE it is ledger-persisted. The aur builder (kit.BuilderReverse)
// echoes back a ReverseOpPackageRemove with an EMPTY UninstallCmd, deferring to this render (the
// caller has the DistroConfig; the out-of-process substrate provider does not).
//
// This test FAILS without the FillReverseUninstallCmds call in recordDeploy: the persisted
// UninstallCmd would be "" instead of the rendered pacman -Rs command.
func TestExternalDeploy_FillsPackageRemoveUninstallCmdOnRecord(t *testing.T) {
	// A minimal DistroConfig carrying the pac format's uninstall_template — the real embedded
	// build vocabulary isn't reachable from this module (no charly-core LoadBuildConfigForBox
	// here), so this constructs the SAME shape directly (DistroConfig.Distro → per-distro
	// ResolvedDistro.Format, matching FindFormat's own walk).
	dc := &buildkit.DistroConfig{
		Distro: map[string]*buildkit.DistroDef{
			"arch": {Format: map[string]*buildkit.FormatDef{
				"pac": {UninstallTemplate: "pacman -Rs --noconfirm {{join .Packages \" \"}}"},
			}},
		},
	}
	distroCfgJSON, err := json.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal distro config: %v", err)
	}

	ledgerRoot := t.TempDir()
	reply := spec.DeployReply{
		Record: spec.DeployReplyRecord{Candy: "chrome", Version: "2026.1.1"},
		ReverseOps: []spec.ReverseOp{{
			Kind:    spec.ReverseOpPackageRemove,
			Format:  "pac",
			Targets: []string{"google-chrome"},
			Scope:   spec.ScopeSystem,
			// UninstallCmd intentionally empty — the exact latent-drop condition.
		}},
	}

	if err := recordDeploy("check-aur-local", "local", distroCfgJSON, ledgerRoot, reply); err != nil {
		t.Fatalf("recordDeploy: %v", err)
	}

	paths, err := ledgerPathsFor(ledgerRoot)
	if err != nil {
		t.Fatalf("ledgerPathsFor: %v", err)
	}
	rec, err := kit.ReadCandyRecord(paths, "chrome")
	if err != nil || rec == nil {
		t.Fatalf("ReadCandyRecord: %v / %+v", err, rec)
	}
	if len(rec.ReverseOps) != 1 {
		t.Fatalf("recorded ReverseOps = %d, want 1: %+v", len(rec.ReverseOps), rec.ReverseOps)
	}
	got := rec.ReverseOps[0].UninstallCmd
	want := "pacman -Rs --noconfirm google-chrome"
	if got != want {
		t.Fatalf("persisted UninstallCmd = %q, want %q (FillReverseUninstallCmds did not run on record)", got, want)
	}
}

// TestMarshalDeployOpParams_NodeSurvivesAsAnObjectNotBase64String proves the R10 bed-found bug:
// storing a plain []byte (the untyped return of json.Marshal) as a map[string]any value makes
// encoding/json base64-encode it — silently turning "node" into an opaque base64 STRING instead
// of an embedded JSON object. The receiving substrate (e.g. candy/plugin-deploy-vm's
// vmPrepareVenue: `var node spec.BundleNode; _ = json.Unmarshal(p.Node, &node)`) then fails to
// decode it (a JSON string into a struct), silently discards the error, and is left with a
// ZERO-VALUE node — exactly the observed symptom (a nested vm child's `From` reading back empty
// even though charly.yml declares it correctly). marshalDeployOpParams must store node as
// json.RawMessage so it splices in as a raw JSON object, never re-encoded.
func TestMarshalDeployOpParams_NodeSurvivesAsAnObjectNotBase64String(t *testing.T) {
	node := &spec.Deploy{From: "eval-vm", Target: "vm"}
	raw, err := marshalDeployOpParams("check-sidecar-pod.check-sidecar-pod-ephvm", "", node, nil)
	if err != nil {
		t.Fatalf("marshalDeployOpParams: %v", err)
	}

	var outer struct {
		Node json.RawMessage `json:"node"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatalf("decode outer params: %v", err)
	}
	if len(outer.Node) == 0 {
		t.Fatalf("outer params carried no \"node\" key at all: %s", raw)
	}
	if outer.Node[0] != '{' {
		t.Fatalf("\"node\" was NOT embedded as a raw JSON object (got %s) — this is the base64-string regression", outer.Node)
	}

	var decoded spec.Deploy
	if err := json.Unmarshal(outer.Node, &decoded); err != nil {
		t.Fatalf("decode node (the exact step vmPrepareVenue performs): %v", err)
	}
	if decoded.From != "eval-vm" {
		t.Fatalf("decoded node.From = %q, want %q", decoded.From, "eval-vm")
	}
}
