package main

import (
	"encoding/json"
	"fmt"
)

// verb_preresolve.go — the GENERIC per-verb host-side preresolver hook (F1), the
// check-verb analogue of deploy_preresolve.go. An out-of-process check verb that
// needs HOST-RESOLVED inputs it cannot compute across the process boundary — a
// cdp/vnc/mcp/spice dialable endpoint resolved from podman/venue/libvirt, or a
// kube op's `cluster:` profile mapped to a kubeconfig context — registers ONE
// preresolver keyed by its verb word here. invokeVerbProvider looks it up and runs
// it, so the verb-invocation path never branches on the verb word: there is NO
// per-verb special-casing in the dispatch (the Uniform API Invariant). The
// resolved opaque payload travels to the plugin in CheckEnv.Substrate (mirroring
// DeployVenue.Substrate); any op rewrite (kube's KubeContext) travels in the op's
// params — both already pure-JSON across the boundary, so a new host-resolved verb
// adds a preresolver here and decodes Substrate in its plugin, with no core edit.

// verbPreresolver resolves a check verb's host-side inputs before its Op is
// marshaled to an out-of-process provider. Returns:
//   - substrate: the opaque JSON the matching plugin decodes (→ CheckEnv.Substrate);
//     nil to ship none (kube rewrites the op instead of shipping a payload).
//   - op: the Op to marshal as params — the endpoint verbs return c unchanged;
//     kube returns c with KubeContext rewritten.
//   - cleanup: tears down any opened tunnel/forward AFTER Invoke (deferred across
//     the call); nil when there is nothing to clean up (mcp/kube).
//   - early: a short-circuit CheckResult (SKIP/FAIL) that ends the verb before
//     dispatch; nil to proceed.
type verbPreresolver func(r *Runner, c *Op) (substrate json.RawMessage, op *Op, cleanup func(), early *CheckResult)

// verbPreresolvers maps a check VERB word → its host-side preresolver. Populated at
// package-var init time (before any init(), like registerDeployPreresolver), so the
// lookup is race-free.
var verbPreresolvers = map[string]verbPreresolver{}

// registerVerbPreresolver records one verb's preresolver. Panics on a duplicate
// word (a startup invariant, like registerDeployPreresolver).
func registerVerbPreresolver(word string, fn verbPreresolver) {
	if word == "" || fn == nil {
		panic("registerVerbPreresolver: empty word or nil preresolver")
	}
	if _, dup := verbPreresolvers[word]; dup {
		panic(fmt.Sprintf("registerVerbPreresolver: duplicate preresolver for %q", word))
	}
	verbPreresolvers[word] = fn
}

// verbPreresolverFor returns the registered preresolver for a verb word, if any. A
// verb with no preresolver (wl/dbus/record/adb/appium/matching/…) ships an empty
// CheckEnv.Substrate and its Op unchanged.
func verbPreresolverFor(word string) (verbPreresolver, bool) {
	fn, ok := verbPreresolvers[word]
	return fn, ok
}
