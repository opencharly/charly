package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestEnsureImage(t *testing.T) {
	// Save and restore original
	orig := LocalImageExists
	defer func() { LocalImageExists = orig }()

	t.Run("same engine image exists", func(t *testing.T) {
		LocalImageExists = func(engine, ref string) bool { return true }
		rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "docker"}
		if err := EnsureImage("myimage:latest", rt); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("same engine image missing", func(t *testing.T) {
		LocalImageExists = func(engine, ref string) bool { return false }
		rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "docker"}
		err := EnsureImage("myimage:latest", rt)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrImageNotLocal) {
			t.Errorf("expected ErrImageNotLocal, got: %v", err)
		}
		if !strings.Contains(err.Error(), "myimage:latest") {
			t.Errorf("expected error to name the missing image, got: %v", err)
		}
	})

	t.Run("cross engine already in run engine", func(t *testing.T) {
		LocalImageExists = func(engine, ref string) bool {
			return engine == "podman" // exists in run engine
		}
		rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "podman"}
		if err := EnsureImage("myimage:latest", rt); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("cross engine missing from both", func(t *testing.T) {
		LocalImageExists = func(engine, ref string) bool { return false }
		rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "podman"}
		err := EnsureImage("myimage:latest", rt)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrImageNotLocal) {
			t.Errorf("expected ErrImageNotLocal, got: %v", err)
		}
	})

	t.Run("cross engine needs transfer", func(t *testing.T) {
		var checks []string
		LocalImageExists = func(engine, ref string) bool {
			checks = append(checks, engine)
			return engine == "docker" // only in build engine
		}
		rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "podman"}
		// TransferImage will fail because no real engines, but we verify
		// the check order: run engine first, then build engine
		_ = EnsureImage("myimage:latest", rt)
		if len(checks) < 2 {
			t.Fatalf("expected at least 2 ImageExists checks, got %d", len(checks))
		}
		if checks[0] != "podman" {
			t.Errorf("first check should be run engine (podman), got %s", checks[0])
		}
		if checks[1] != "docker" {
			t.Errorf("second check should be build engine (docker), got %s", checks[1])
		}
	})

	t.Run("podman to docker transfer", func(t *testing.T) {
		// This test requires docker to be in PATH (it execs "docker load")
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not available, skipping cross-engine transfer test")
		}
		LocalImageExists = func(engine, ref string) bool {
			return engine == "podman" // only in build engine
		}
		rt := &ResolvedRuntime{BuildEngine: "podman", RunEngine: "docker"}
		// TransferImage will fail (no real engines), but we verify EnsureImage
		// attempts the transfer in the right direction
		err := EnsureImage("myimage:latest", rt)
		// The error comes from TransferImage trying to exec podman save
		if err == nil {
			t.Fatal("expected error from TransferImage (no real engine)")
		}
		// It should NOT be a "not found" error — it should be a transfer error
		if strings.Contains(err.Error(), "not found") {
			t.Errorf("should have attempted transfer, not reported not-found: %v", err)
		}
	})
}
