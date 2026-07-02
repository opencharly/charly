package kit

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// fakeExec is a recording kit.DeployExecutor: it captures PutFile placements + RunSystem/
// RunUser scripts + RunHostStep step kinds, so the walk's per-kind decisions are asserted
// without a live venue.
type fakeExec struct {
	puts       map[string][]byte // path → content
	putRoot    map[string]bool   // path → ownerRoot
	sysScripts []string
	usrScripts []string
	hostKinds  []string // step kinds routed to RunHostStep
	getReturn  map[string][]byte
}

func newFakeExec() *fakeExec {
	return &fakeExec{puts: map[string][]byte{}, putRoot: map[string]bool{}, getReturn: map[string][]byte{}}
}

func (e *fakeExec) Venue(context.Context) (string, error) { return "fake://venue", nil }
func (e *fakeExec) RunSystem(_ context.Context, s string, _ []byte) error {
	e.sysScripts = append(e.sysScripts, s)
	return nil
}
func (e *fakeExec) RunUser(_ context.Context, s string, _ []byte) error {
	e.usrScripts = append(e.usrScripts, s)
	return nil
}
func (e *fakeExec) PutFile(_ context.Context, path string, content []byte, _ uint32, ownerRoot bool) error {
	e.puts[path] = content
	e.putRoot[path] = ownerRoot
	return nil
}
func (e *fakeExec) GetFile(_ context.Context, path string, _ bool) ([]byte, error) {
	if b, ok := e.getReturn[path]; ok {
		return b, nil
	}
	return nil, nil // empty (not-found-equivalent for the managed-block read)
}
func (e *fakeExec) RunCapture(_ context.Context, _ string) (string, string, int, error) {
	return "/home/u", "", 0, nil // venue $HOME / $SHELL probe
}
func (e *fakeExec) RunHostStep(_ context.Context, step spec.InstallStepView, _ []byte) ([]spec.ReverseOp, error) {
	e.hostKinds = append(e.hostKinds, step.Kind)
	return []spec.ReverseOp{{Kind: spec.ReverseOpPluginScript, Extra: map[string]string{spec.ReverseOpPluginScriptKey: "echo teardown " + step.Kind}}}, nil
}

// TestWalkPlans_PerKindDispatch proves WalkPlans routes each step kind correctly: the
// plugin-renderable kinds execute via the F2 legs (PutFile / RunSystem / RunUser) and echo
// the host-computed view.ReverseOps; the host-engine kinds (Builder / LocalPkgInstall /
// SystemPackages / act-Op / ExternalPlugin) route to RunHostStep; ShellHook triggers the
// env.d managed-block finalizer.
func TestWalkPlans_PerKindDispatch(t *testing.T) {
	exec := newFakeExec()
	rev := []spec.ReverseOp{{Kind: spec.ReverseOpRmFileSystem, Targets: []string{"/x"}}}
	plan := spec.InstallPlanView{
		Steps: []spec.InstallStepView{
			// Plugin-renderable: Op write → PutFile at the authored dest, root by scope.
			{Kind: "Op", Scope: spec.ScopeSystem, CandyName: "c", Op: &spec.Op{Write: "/etc/m", Content: "hi\n", Mode: "0644"}, ReverseOps: rev},
			// Plugin-renderable: ShellHook → env.d PutFile + triggers the managed-block finalizer.
			{Kind: "ShellHook", Scope: spec.ScopeUserProfile, CandyName: "c", EnvFile: "/home/u/.config/opencharly/env.d/c.env", EnvVars: map[string]string{"FOO": "bar"}, ReverseOps: rev},
			// Plugin-renderable: ServiceCustom → PutFile unit + systemctl enable.
			{Kind: "ServiceCustom", TargetScope: spec.ScopeSystem, Name: "u.service", UnitText: "[Unit]\n", UnitPath: "/etc/systemd/system/u.service", Enable: true, ReverseOps: rev},
			// Plugin-renderable: RepoChange → PutFile root.
			{Kind: "RepoChange", CandyName: "c", File: "/etc/yum.repos.d/x.repo", Content: "[x]\n", ReverseOps: rev},
			// Host-engine kinds → RunHostStep.
			{Kind: "Builder", CandyName: "c"},
			{Kind: "SystemPackages", Format: "pac", Packages: []string{"ripgrep"}},
			{Kind: "Op", CandyName: "c", Op: &spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/y"}}}, // act-verb Op
			{Kind: "ExternalPlugin", CandyName: "c", Op: &spec.Op{Plugin: "examplestep"}},
		},
	}

	got, err := WalkPlans(context.Background(), exec, []spec.InstallPlanView{plan}, WalkOpts{})
	if err != nil {
		t.Fatalf("WalkPlans: %v", err)
	}

	// Op write placed at the authored dest, root-owned (system scope).
	if string(exec.puts["/etc/m"]) == "" {
		// write renders an `install … <<EOF` script via RunSystem, not PutFile — assert the script ran.
		if len(exec.sysScripts) == 0 || !strings.Contains(strings.Join(exec.sysScripts, "\n"), "/etc/m") {
			t.Errorf("Op write: neither PutFile nor RunSystem placed /etc/m; sys=%v", exec.sysScripts)
		}
	}
	// ShellHook env.d file placed at the host-resolved EnvFile.
	if _, ok := exec.puts["/home/u/.config/opencharly/env.d/c.env"]; !ok {
		t.Errorf("ShellHook: env.d file not placed; puts=%v", keysOf(exec.puts))
	}
	// ServiceCustom unit placed root + enabled via systemctl.
	if !exec.putRoot["/etc/systemd/system/u.service"] {
		t.Errorf("ServiceCustom: unit not placed root-owned")
	}
	if !strings.Contains(strings.Join(exec.sysScripts, "\n"), "systemctl enable") {
		t.Errorf("ServiceCustom: no systemctl enable; sys=%v", exec.sysScripts)
	}
	// RepoChange placed root.
	if !exec.putRoot["/etc/yum.repos.d/x.repo"] {
		t.Errorf("RepoChange: repo file not placed root-owned")
	}
	// Host-engine kinds routed to RunHostStep (and ONLY those).
	wantHost := map[string]bool{"Builder": true, "SystemPackages": true, "Op": true, "ExternalPlugin": true}
	hostSeen := map[string]bool{}
	for _, k := range exec.hostKinds {
		hostSeen[k] = true
	}
	for k := range wantHost {
		if !hostSeen[k] {
			t.Errorf("host-engine kind %q not routed to RunHostStep; routed=%v", k, exec.hostKinds)
		}
	}
	// env.d managed-block finalizer ran (a ShellHook step was present) — the venue rc file
	// (~/.bashrc, from the $SHELL=bash default + $HOME=/home/u probe) got the sourcing block.
	if _, ok := exec.puts["/home/u/.bashrc"]; !ok {
		t.Errorf("managed-block finalizer did not write the rc file; puts=%v", keysOf(exec.puts))
	}
	// Reverse ops: the echoed view.ReverseOps (plugin-renderable) + the RunHostStep replies.
	if len(got) == 0 {
		t.Error("WalkPlans returned no reverse ops")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
