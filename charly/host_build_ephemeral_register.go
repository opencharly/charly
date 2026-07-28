package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// host_build_ephemeral_register.go — the "ephemeral-register" F10 host-builder (FINAL/K5 unit
// 6a): wraps registerEphemeralIfMarked verbatim behind a generic, substrate-agnostic HostBuild
// seam, so a substrate whose venue-lifecycle PrepareVenue body now lives in its OWN plugin
// (candy/plugin-deploy-vm, replacing the deleted lifecyclePrepareHook indirection) can still
// trigger the ONE Add-time host side effect it cannot do itself: the systemd transient-timer
// registration + panic-vs-warning classification (RCA #5) that must stay single-sourced
// host-side, where the add-failure semantics live. The seam kind is a generic action noun (F11)
// — never a substrate word — so pod/k8s's own future ephemeral support (the bed-robustness
// batch) reaches it through the identical hop, no new mechanism per substrate.
//
// FLOOR-M adjudication (Cone A shape 3, relocated verbatim from the deleted
// charly/deploy_add_shared.go — the prior header there flagged this as "candidate-floor,
// pending FLOOR-SLIM adjudication"; this is that adjudication): registerEphemeralIfMarked wraps
// RegisterEphemeralLifecycle, which ALREADY dispatches into candy/plugin-bundle via the provider
// registry (ephemeral_dispatch.go's dispatchEphemeralOp) — the plugin-reaching part is done.
// What is left here is pure host bookkeeping (the panic-vs-warning classification) around a call
// that already reaches the plugin; moving it further would only relocate a warn/error switch
// with no boundary-law benefit, and it is the SOLE caller of both functions.
const ephemeralRegisterBuilderKind = "ephemeral-register"

func hostBuildEphemeralRegister(_ context.Context, req spec.EphemeralRegisterRequest, _ buildEngineContext) (spec.EphemeralRegisterReply, error) {
	if err := registerEphemeralIfMarked(req.Node, req.Name); err != nil {
		return spec.EphemeralRegisterReply{}, err
	}
	return spec.EphemeralRegisterReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(ephemeralRegisterBuilderKind, typedHostBuilder(ephemeralRegisterBuilderKind, hostBuildEphemeralRegister))
	return true
}()

// registerEphemeralIfMarked runs the ephemeral lifecycle registration
// (systemd transient timer + parent-detection) when the dispatch-merged
// node is ephemeral. Called as the FIRST action of vm's Add (panic-safe TTL
// ordering) — the ONLY substrate that calls it today (vm_lifecycle_preresolve.go);
// pod/k8s Add never reach it (a pre-existing gap, tracked to the bed-robustness
// batch — see validate_ephemeral.go for the load-time guard that makes the gap
// LOUD instead of silent). Consumes the merged node — does NOT re-read charly.yml.
// Dispatches to command:bundle's OpEphemeralRegister (ephemeral_dispatch.go,
// FINAL/K5 unit 6a) — the registration BODY moved to candy/plugin-bundle.
//
// RCA #5 (FINAL/K5 unit 6a, live-probe-caught): an ORDINARY registration error (e.g.
// systemd-run missing — an expected condition) stays a soft, logged warning, matching the
// prior run* behavior. A PANIC-CLASS error (sdk.EphemeralPanicMarker — the plugin's
// recoverEphemeralOpPanic converts an unrecovered panic into this marker, since a bare Go
// panic previously crashed silently or vanished before reaching this caller — team-lead's
// probe caught a nil-map write panic in persistEphemeralRuntime that a bed run reported as
// PASS) is NEVER a soft warning: it signals a genuine bug, so it is returned to FAIL the
// whole Add — "a panicking registration must fail the add, not vanish."
func registerEphemeralIfMarked(node *spec.BundleNode, name string) error {
	if node == nil || !node.IsEphemeral() {
		return nil
	}
	regErr := RegisterEphemeralLifecycle(node, name)
	if regErr == nil {
		return nil
	}
	if isEphemeralPanicError(regErr) {
		return fmt.Errorf("ephemeral lifecycle registration: %w", regErr)
	}
	fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
	return nil
}

// isEphemeralPanicError reports whether err was converted from a recovered panic (carries
// sdk.EphemeralPanicMarker — candy/plugin-bundle's recoverEphemeralOpPanic) rather than an
// ordinary registration condition. Pulled out as its own pure function purely for testability
// (registerEphemeralIfMarked's own caller, RegisterEphemeralLifecycle, is seam-coupled — needs
// the live provider registry — not unit-testable standalone).
func isEphemeralPanicError(err error) bool {
	return err != nil && strings.Contains(err.Error(), sdk.EphemeralPanicMarker)
}
