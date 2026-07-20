package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// The act renderer produces the create/configure command for each
// state-provision verb, and declines (ok=false) for observe-only verbs.
// Act-ness is the enclosing step's `run:` keyword (intentDo=act); the verb
// provider's ProvisionActor renders it, so the Op carries no `do:` field anymore.
//
// actScriptForTest resolves a verb to its provider and renders the
// act script via ProvisionActor — the same path runProvisionAct takes (C1b).
func actScriptForTest(op *spec.Op, verb string, distros []string) (string, bool) {
	prov, ok := providerRegistry.ResolveVerb(verb)
	if !ok {
		return "", false
	}
	actor, ok := prov.(ProvisionActor)
	if !ok {
		return "", false
	}
	return actor.RenderProvisionScript(op, distros)
}

func TestRenderProvisionScript(t *testing.T) {
	cases := []struct {
		name     string
		op       spec.Op
		verb     string
		wantOK   bool
		contains string
	}{
		// package and service are extracted state-provision verbs. Their build/deploy install
		// timeline lowers into a typed SystemPackagesStep / ServicePackagedStep (compileActOp),
		// but each ALSO keeps a ProvisionActor for the runtime/box-build live-act path, which
		// decodes plugin_input.
		{"package", spec.Op{Plugin: "package", PluginInput: map[string]any{"package": "redis"}}, "package", true, "install"},
		{"service", spec.Op{Plugin: "service", PluginInput: map[string]any{"service": "sshd"}}, "service", true, "enable --now"},
		// file is an extracted state-provision verb: a builtin plugin unit whose provider is a
		// ProvisionActor reading plugin_input (file/mode) + the SHARED content off the step Op.
		{"file-content", spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/x.conf", "mode": "0644"}, Content: "hi\n"}, "file", true, "chmod '0644'"},
		// unix_group / user / kernel-param / mount are extracted state-provision verbs:
		// builtin plugin units whose providers are ProvisionActors reading plugin_input (not
		// the removed Op.UnixGroup/User/KernelParam/Mount/… fields).
		{"unix_group", spec.Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "devs"}}, "unix_group", true, "groupadd"},
		{"user", spec.Op{Plugin: "user", PluginInput: map[string]any{"user": "bob"}}, "user", true, "useradd"},
		{"kernel-param", spec.Op{Plugin: "kernel-param", PluginInput: map[string]any{"kernel-param": "vm.swappiness", "value": "10"}}, "kernel-param", true, "sysctl -w 'vm.swappiness'='10'"},
		{"mount", spec.Op{Plugin: "mount", PluginInput: map[string]any{"mount": "/mnt/data", "mount_source": "/dev/sdb1", "filesystem": "ext4"}}, "mount", true, "mount -t 'ext4' '/dev/sdb1' '/mnt/data'"},
		// An observe-only verb has no act form → ok=false (falls to the probe handler).
		// addr is now a builtin plugin verb (authored plugin: addr + plugin_input); its
		// provider is a CheckVerbProvider, not a ProvisionActor, so it still has no act form.
		{"addr-observe", spec.Op{Plugin: "addr", PluginInput: map[string]any{"addr": "127.0.0.1:80", "reachable": true}}, "addr", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := actScriptForTest(&c.op, c.verb, nil)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (script=%q)", ok, c.wantOK, got)
			}
			if c.wantOK && !strings.Contains(got, c.contains) {
				t.Errorf("script %q does not contain %q", got, c.contains)
			}
		})
	}
}

// TestRunProvisionActDispatch exercises the FULL act path end-to-end (C1b): the
// verb resolves to its provider, ProvisionActor renders the script, and the
// rendered script runs via the executor — pass on exit 0, fail on non-zero, and
// ok=false (fall through) for a verb with no act form.
func TestRunProvisionActDispatch(t *testing.T) {
	ctx := context.Background()

	// file is a ProvisionActor → renders `mkdir … && touch …`, execs, passes. The verb is
	// now the generic plugin: file (Kind()=plugin → Op.Plugin=file resolves the provider).
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "mkdir", exit: 0}}}
	r := hostVerbResolverFor(fe, RunModeLive)
	res, ok := r.runProvisionAct(ctx, &spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/tmp/x"}}, "file")
	if !ok {
		t.Fatalf("runProvisionAct(file) ok=false, want true (file is a ProvisionActor)")
	}
	if res.Status != TestPass {
		t.Fatalf("runProvisionAct(file) status=%v, want TestPass; msg=%q", res.Status, res.Message)
	}
	if len(fe.calls) != 1 || !strings.Contains(fe.calls[0], "mkdir") {
		t.Fatalf("expected the rendered script to execute once; calls=%v", fe.calls)
	}

	// addr has no act form → ok=false (caller falls through to the probe handler). addr is
	// now a builtin plugin verb; its CheckVerbProvider is not a ProvisionActor.
	if _, ok := r.runProvisionAct(ctx, &spec.Op{Plugin: "addr", PluginInput: map[string]any{"addr": "127.0.0.1:80"}}, "addr"); ok {
		t.Fatalf("runProvisionAct(addr) ok=true, want false (no act form)")
	}

	// An unknown verb (no provider) → ok=false.
	if _, ok := r.runProvisionAct(ctx, &spec.Op{}, "no-such-verb"); ok {
		t.Fatalf("runProvisionAct(unknown) ok=true, want false")
	}

	// Non-zero exit → fail.
	feFail := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "mkdir", exit: 1, stderr: "boom"}}}
	rFail := hostVerbResolverFor(feFail, RunModeLive)
	res2, ok := rFail.runProvisionAct(ctx, &spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/tmp/y"}}, "file")
	if !ok || res2.Status != TestFail {
		t.Fatalf("runProvisionAct(file, exit 1) = (status=%v, ok=%v), want (TestFail, true)", res2.Status, ok)
	}
}
