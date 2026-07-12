package build

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// testPodmanJobs is the host-resolved `podman build --jobs` value the candy drive
// reads verbatim from driveConfig.PodmanJobs (the host build-resolve reply computes
// it; the drive no longer runs resolvePodmanJobs). A fixed value keeps these
// arg-shape assertions independent of the host CPU count.
const testPodmanJobs = 4

var testJobsStr = strconv.Itoa(testPodmanJobs)

func TestBuildLocalArgs(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64"}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415", "ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:2026.046.1415",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsPodman(t *testing.T) {
	c := driveConfig{Engine: "podman", Platform: "linux/arm64", PodmanJobs: testPodmanJobs}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/arm64",
		"--jobs", testJobsStr,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgs(t *testing.T) {
	c := driveConfig{Push: true}
	args := c.buildDockerPushArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415", "ghcr.io/opencharly/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:2026.046.1415",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/opencharly/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/opencharly/cache:fedora,mode=max,compression=zstd",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithGHACache(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64", Cache: "gha"}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithGHACache(t *testing.T) {
	c := driveConfig{Cache: "gha"}
	args := c.buildDockerPushArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithRegistryCache(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64", Cache: "registry"}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=registry,ref=ghcr.io/opencharly/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/opencharly/cache:fedora,mode=max,compression=zstd",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithRegistryCache(t *testing.T) {
	c := driveConfig{Cache: "registry"}
	args := c.buildDockerPushArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/opencharly/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/opencharly/cache:fedora,mode=max,compression=zstd",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildRegistryCacheNoRegistry(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64", Cache: "registry"}
	args := c.buildLocalArgs(
		[]string{"fedora:latest"},
		"fedora", "")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry, no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgs(t *testing.T) {
	c := driveConfig{EngineName: "podman", Push: true, PodmanJobs: testPodmanJobs}
	args := c.buildPodmanPushArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"--manifest", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/amd64,linux/arm64",
		"--jobs", testJobsStr,
		"--cache-from", "ghcr.io/opencharly/fedora",
		"--cache-to", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithImageCache(t *testing.T) {
	c := driveConfig{Engine: "podman", Platform: "linux/amd64", Cache: "image", PodmanJobs: testPodmanJobs}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--jobs", testJobsStr,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithImageCache(t *testing.T) {
	c := driveConfig{Cache: "image"}
	args := c.buildDockerPushArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		[]string{"linux/amd64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgsWithImageCache(t *testing.T) {
	c := driveConfig{EngineName: "podman", Push: true, Cache: "image", PodmanJobs: testPodmanJobs}
	args := c.buildPodmanPushArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		[]string{"linux/amd64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"--manifest", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/amd64",
		"--jobs", testJobsStr,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildImageCacheNoRegistry(t *testing.T) {
	c := driveConfig{Engine: "podman", Platform: "linux/amd64", Cache: "image", PodmanJobs: testPodmanJobs}
	args := c.buildLocalArgs(
		[]string{"fedora:latest"},
		"fedora", "")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		"--jobs", testJobsStr,
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image, no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDefaultCacheNoRegistry(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64"}
	args := c.buildLocalArgs(
		[]string{"fedora:latest"},
		"fedora", "")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildNoCache(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64", NoCache: true}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(no-cache) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildCacheNone(t *testing.T) {
	c := driveConfig{Engine: "docker", Platform: "linux/amd64", Cache: "none"}
	args := c.buildLocalArgs(
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"docker", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(cache=none) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestRetryCmdSucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := retryCmd(time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryCmdSucceedsAfterRetries(t *testing.T) {
	calls := 0
	err := retryCmd(time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryCmdExhaustsAttempts(t *testing.T) {
	calls := 0
	err := retryCmd(time.Millisecond, func() error {
		calls++
		return fmt.Errorf("persistent error")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}
