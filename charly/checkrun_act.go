package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// checkrun_act.go — the runtime do:act execution path.
//
// runOne dispatches by the op's resolved do-mode (Op.EffectiveDo, stamped from
// the enclosing Step keyword):
//
//   - do: assert (check:) → the run<Verb> probe handlers (the default).
//   - do: act    (run:)   → for a STATE-PROVISION verb (file/package/service/
//                   user/group/kernel-param/mount) render the create/configure
//                   command and run it via the executor. ACTION verbs
//                   (command/http/dbus/cdp/wl/vnc/mcp/k8s/adb/appium/spice/
//                   libvirt/record/kill) already perform their side-effect in
//                   their own handler, so do:act there reuses that handler.
//
// The state-provision renderers are the do:act half of each verb provider — a
// ProvisionActor method (verb_builtins.go types). runProvisionAct resolves the
// verb through the registry and type-asserts ProvisionActor; the per-verb switch
// is gone (C1b).
//
// Agent steps (agent-run:/agent-check:) never reach runOne — they route to the
// grader in runUnit (description_run.go). Runtime act ops are NOT auto-reversed
// (no ledger entry) — the author reverses them with a teardown run: step.

// resolveProvisionScript resolves an op's state-provision verb to its ProvisionActor
// and renders the act shell — the SINGLE Op→act-shell seam shared by the runtime act
// path (runProvisionAct) AND every install-emit path: emitTasks' `case "plugin"` (the
// box build via writeCandySteps→emitTasks, and the pod overlay via candy/plugin-installstep's
// step:op OpEmit → the host step-emit seam → stepEmitOp → emitTasks) AND renderOpCommand
// (the local/vm deploy targets) — the
// act-emit enabler, so a state-provision verb provisions identically whether run live,
// baked into an image, or applied at deploy (R3).
//
// It threads the plugin indirection: when the op's verb is the generic `plugin:`
// discriminator, the ProvisionActor is the plugin word's provider (op.Plugin), NOT the
// pluginVerb dispatcher. ok=false when the resolved provider is not a ProvisionActor (an
// action verb whose handler already acts, a pure observe verb, or a non-act plugin) — the
// runtime caller then falls through to the normal dispatch; an emit caller turns it into a
// hard error (a run: step naming a non-act verb has no build/deploy install path).
func resolveProvisionScript(op *spec.Op, distros []string) (string, bool) {
	word, err := op.Kind()
	if err != nil {
		return "", false
	}
	if word == "plugin" {
		word = op.Plugin
	}
	prov, ok := providerRegistry.ResolveVerb(word)
	if !ok {
		return "", false
	}
	actor, ok := prov.(ProvisionActor)
	if !ok {
		return "", false
	}
	return actor.RenderProvisionScript(op, distros)
}

// runProvisionAct executes a state-provision verb's create/configure command
// and reports pass on a zero exit. Returns ok=false when the verb has no
// provision renderer (an action verb whose handler already acts, or a pure
// observe verb) so the caller falls through to the normal dispatch. Resolution
// (incl. the `plugin:` indirection) is the shared resolveProvisionScript.
func (h *hostVerbResolver) runProvisionAct(ctx context.Context, c *spec.Op, verb string) (spec.CheckResult, bool) {
	script, ok := resolveProvisionScript(c, h.kr.Distros())
	if !ok {
		return spec.CheckResult{}, false
	}
	if h.kr.Mode() == RunModeBox {
		return skipf(c, "do: act not meaningful under charly check box (no running target)"), true
	}
	_, stderr, exit, err := h.kr.Exec().RunCapture(ctx, kit.WrapContainerCommand(script))
	if err != nil {
		return failf(c, "act %s: execution error: %v", verb, err), true
	}
	if exit != 0 {
		return failf(c, "act %s: exit %d: %s", verb, exit, strings.TrimSpace(stderr)), true
	}
	return passf(c, fmt.Sprintf("act %s: applied", verb)), true
}

// The do:act renderers — the ProvisionActor half of each state-provision verb
// provider — live with their providers in their relocated compiled-in candies
// (candy/plugin-package, candy/plugin-service, candy/plugin-file, candy/plugin-user,
// candy/plugin-unix-group, candy/plugin-kernel-param, candy/plugin-mount, each implementing
// kit.ProvisionActor with a candy-private matcher codec). Each decodes
// plugin_input rather than the removed
// Op.File/Exists/Owner/GroupOf/Filetype/Contains/Sha256 (mode + content stay SHARED #Op
// modifiers the file act reads off the step Op) / Op.Package/Installed/Versions/PackageMap
// / Op.Service/Running/Enabled / Op.User/UID/Home/Shell / Op.UnixGroup/GID /
// Op.KernelParam/Value / Op.Mount/MountSource/Filesystem/Opts fields. `package`'s and
// `service`'s RenderProvisionScript are the RUNTIME/box-build live-act path (a
// `run: {plugin: package}` / `run: {plugin: service}` step the check Runner executes,
// plus the box-build emitTasks `case "plugin"` seam) — their build/deploy install timeline
// lowers into a TYPED SystemPackagesStep / ServicePackagedStep via the TypedStepProvider
// (compileActOp), NOT this shell.

// renderOpCommand turns a non-copy OpStep into a shell command. The structured verbs
// (command/plugin:command/mkdir/link/setcap/write/download) render via the SHARED pure
// kit.RenderOpCommand; an act-`plugin:` verb (a builtin ProvisionActor) renders via the
// in-proc registry (resolveProvisionScript, above) — the SAME seam the build/runtime act
// paths use (R3). copy is staged via the executor's PutFile, never rendered. The ONE
// op→shell render copy is kit's; the in-proc deploy path calls this wrapper, an
// out-of-process deploy plugin's kit.WalkPlans calls kit.RenderOpCommand directly.
func renderOpCommand(s *deploykit.OpStep) (string, error) {
	if s.Op == nil {
		return "", fmt.Errorf("renderOpCommand: nil op")
	}
	if s.Op.Copy != "" {
		return "", fmt.Errorf("copy: task must be staged via PutFile, not rendered")
	}
	if cmd, handled := kit.RenderOpCommand(s.Op, s.CtxPath, s.CandyVars); handled {
		return cmd, nil
	}
	// Not a pure-renderable verb → an act-`plugin:` verb whose ProvisionActor shell needs
	// the in-proc registry. ok=false means the verb has no act form (a run: step naming a
	// non-act verb has no install path — a hard authoring error).
	script, ok := resolveProvisionScript(s.Op, s.Distros)
	if !ok {
		return "", fmt.Errorf("run: plugin verb %q is not act-capable (no ProvisionActor)", s.Op.Plugin)
	}
	return script, nil
}
