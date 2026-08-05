package deploypod

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// image_ensure_test.go — ported from the former charly/ensure_image_test.go's
// TestEnsureImagePresent (K-wave W3a B6). The subject's signature changed: the run engine is now
// hardcoded "podman" (matching the deleted host body's own podmanRT.RunEngine hardcode — a pod
// deploy always runs on podman), so cases exercised with an explicit RunEngine now pass buildEngine
// only. Tier-3 (build:ensure peer-dispatch) needs a live *sdk.Executor to avoid a nil-pointer
// panic in InvokeProvider (spec/exec.Executor.client) — unlike core's former
// providerRegistry.resolve, which degraded gracefully with ok=false in a bare test binary. Those
// subtests use the SAME recover-based nil-executor pattern already established in this package
// (config_setup_test.go's TestRunPodConfigSetup_DirectModeAllowed) since a genuine fake executor
// is unwarranted for what tier-1/tier-2 already prove; only "does it REACH tier 3" is asserted
// there, not tier 3's own result (build:ensure's own behavior is out of this package's scope).

func TestEnsureImagePresent(t *testing.T) {
	orig := container.LocalImageExists
	defer func() { container.LocalImageExists = orig }()

	t.Run("run engine has image", func(t *testing.T) {
		container.LocalImageExists = func(engine, ref string) bool { return engine == podRunEngine }
		if err := ensureImagePresent(t.Context(), nil, "myimage:latest", "podman"); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("cross engine already in run engine", func(t *testing.T) {
		container.LocalImageExists = func(engine, ref string) bool { return engine == podRunEngine }
		if err := ensureImagePresent(t.Context(), nil, "myimage:latest", "docker"); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("cross engine needs transfer, check order", func(t *testing.T) {
		var checks []string
		container.LocalImageExists = func(engine, ref string) bool {
			checks = append(checks, engine)
			return engine == "docker" // only in build engine
		}
		// TransferImage will fail (no real docker/podman), but we verify the check order:
		// run engine (podman) first, then build engine (docker).
		_ = ensureImagePresent(t.Context(), nil, "myimage:latest", "docker")
		if len(checks) < 2 {
			t.Fatalf("expected at least 2 ImageExists checks, got %d", len(checks))
		}
		if checks[0] != podRunEngine {
			t.Errorf("first check should be run engine (%s), got %s", podRunEngine, checks[0])
		}
		if checks[1] != "docker" {
			t.Errorf("second check should be build engine (docker), got %s", checks[1])
		}
	})

	t.Run("podman to docker transfer attempted", func(t *testing.T) {
		// This test requires docker to be in PATH (it execs "docker load"); podRunEngine is
		// always "podman", so this exercises the transfer FROM docker TO podman (the deleted
		// core test's "podman to docker transfer" case inverted RunEngine to docker — no longer
		// expressible now that RunEngine is hardcoded, so this proves the same transfer LEG
		// instead: buildEngine=docker, runEngine=podman, image present only in docker).
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not available, skipping cross-engine transfer test")
		}
		container.LocalImageExists = func(engine, ref string) bool { return engine == "docker" }
		err := ensureImagePresent(t.Context(), nil, "myimage:latest", "docker")
		if err == nil {
			t.Fatal("expected error from TransferImage (no real engine)")
		}
		if strings.Contains(err.Error(), "not found") {
			t.Errorf("should have attempted transfer, not reported not-found: %v", err)
		}
	})

	t.Run("image missing everywhere reaches build:ensure tier", func(t *testing.T) {
		container.LocalImageExists = func(engine, ref string) bool { return false }
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Reached dispatchBuildEnsurePeer's ex.InvokeProvider with a nil test
					// executor — proves tier 1+2 were correctly exhausted first, matching
					// the deleted core test's "cross engine missing from both" assertion
					// that ALL tiers were tried before failing. build:ensure's own
					// resolve/decode behavior is out of this package's test scope.
					err = errors.New("reached build:ensure tier (nil-executor panic, expected)")
				}
			}()
			err = ensureImagePresent(t.Context(), nil, "myimage:latest", "podman")
		}()
		if err == nil {
			t.Fatal("expected an error (either spec.ErrImageNotLocal or the tier-3 panic marker)")
		}
		if !errors.Is(err, spec.ErrImageNotLocal) && err.Error() != "reached build:ensure tier (nil-executor panic, expected)" {
			t.Errorf("expected spec.ErrImageNotLocal or the tier-3 reach marker, got: %v", err)
		}
	})
}
