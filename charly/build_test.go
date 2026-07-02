package main

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// cpuJobs is the --jobs value expected in assembled podman args. It uses the
// same cap logic as the production code (resolvePodmanJobs with override=0 and
// no configured cap → podmanJobsCapFallback) so these tests stay correct
// regardless of the host's actual NCPU count and the fallback constant value.
var cpuJobs = strconv.Itoa(resolvePodmanJobs(0, 0))

func TestBuildLocalArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415", "ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
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
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("podman",
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		"linux/arm64", "fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/arm64",
		"--jobs", cpuJobs,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgs(t *testing.T) {
	cmd := &BuildCmd{Push: true}
	args := cmd.buildDockerPushArgs(
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
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
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
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildDockerPushArgs(
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
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
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
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildDockerPushArgs(
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
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
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
	cmd := &BuildCmd{Push: true}
	args := cmd.buildPodmanPushArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"--manifest", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/amd64,linux/arm64",
		"--jobs", cpuJobs,
		"--cache-from", "ghcr.io/opencharly/fedora",
		"--cache-to", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestFilterImages(t *testing.T) {
	images := map[string]*ResolvedBox{
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
		},
		"fedora-test": {
			Name:           "fedora-test",
			Base:           "fedora",
			IsExternalBase: false,
		},
		"ubuntu": {
			Name:           "ubuntu",
			IsExternalBase: true,
		},
	}

	order := []string{"fedora", "ubuntu", "fedora-test"}

	// Request only fedora-test — should pull in fedora as dependency
	filtered, err := filterBox(order, []string{"fedora-test"}, images)
	if err != nil {
		t.Fatalf("filterBox() error: %v", err)
	}
	want := []string{"fedora", "fedora-test"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterBox() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesUnknown(t *testing.T) {
	images := map[string]*ResolvedBox{
		"fedora": {Name: "fedora", IsExternalBase: true},
	}
	_, err := filterBox([]string{"fedora"}, []string{"nonexistent"}, images)
	if err == nil {
		t.Error("expected error for unknown image")
	}
}

func TestFilterImagesIncludesBuilder(t *testing.T) {
	images := map[string]*ResolvedBox{
		"builder": {
			Name:           "builder",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
			Builder:        BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
			Builder:        BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	order := []string{"builder", "fedora", "app"}

	// Request only app — should pull in fedora (base) and builder
	filtered, err := filterBox(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("filterBox() error: %v", err)
	}
	want := []string{"builder", "fedora", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterBox() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesIncludesBootstrapBuilder(t *testing.T) {
	// Regression: 2026-05 cachyos / cachyos-pacstrap-builder bug. Requesting
	// the downstream `app` (base: cachyos) must pull cachyos-pacstrap-builder
	// into the filtered set even though it's referenced via the dedicated
	// BootstrapBuilderImage field, not via Builder map. Without this, the
	// `charly update --build versa` path silently skipped scheduling
	// cachyos-pacstrap-builder, and runPrivilegedBootstrap then hard-failed
	// at resolveLocalImageRef with "build the bootstrap_builder_image first".
	images := map[string]*ResolvedBox{
		"arch": {
			Name:           "arch",
			IsExternalBase: true,
		},
		"cachyos-pacstrap-builder": {
			Name:           "cachyos-pacstrap-builder",
			Base:           "arch",
			IsExternalBase: false,
		},
		"cachyos": {
			Name:                  "cachyos",
			IsExternalBase:        true,
			BootstrapBuilderImage: "cachyos-pacstrap-builder",
		},
		"app": {
			Name:           "app",
			Base:           "cachyos",
			IsExternalBase: false,
		},
	}

	order := []string{"arch", "cachyos-pacstrap-builder", "cachyos", "app"}

	filtered, err := filterBox(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("filterBox() error: %v", err)
	}
	want := []string{"arch", "cachyos-pacstrap-builder", "cachyos", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterBox() = %v, want %v", filtered, want)
	}
}

func TestBuildLocalArgsWithImageCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildLocalArgs("podman",
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "ghcr.io/opencharly/fedora:latest",
		"--platform", "linux/amd64",
		"--jobs", cpuJobs,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithImageCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildDockerPushArgs(
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
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildPodmanPushArgs(
		[]string{"ghcr.io/opencharly/fedora:2026.046.1415"},
		[]string{"linux/amd64"},
		"fedora", "ghcr.io/opencharly")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"--manifest", "ghcr.io/opencharly/fedora:2026.046.1415",
		"--platform", "linux/amd64",
		"--jobs", cpuJobs,
		"--cache-from", "ghcr.io/opencharly/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildImageCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildLocalArgs("podman",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
	want := []string{
		"podman", "build", "--layers=true", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		"--jobs", cpuJobs,
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image, no registry) =\n  %v\nwant\n  %v", args, want)
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

func TestBuildDefaultCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
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
	cmd := &BuildCmd{NoCache: true}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
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
	cmd := &BuildCmd{Cache: "none"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/opencharly/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/opencharly")
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

func TestHostPlatform(t *testing.T) {
	p := hostPlatform()
	// Should start with linux/
	if p != "linux/amd64" && p != "linux/arm64" {
		t.Logf("hostPlatform() = %q (non-standard arch, that's OK)", p)
	}
}

// TestRenderPacstrapExtraConf locks in the shared pacstrap pacman.conf renderer:
// (1) a microarch repo (CachyOS x86_64_v3) yields an [options] Architecture
// directive — the fix for "package architecture is not valid"; (2) per-repo
// SigLevel is always emitted — the fix for the VM bootstrap path dropping it
// (GPGME "No data" on SigLevel=Never repos); (3) non-microarch / empty inputs
// stay clean (no spurious [options], no regression for arch-pacstrap).
func TestRenderPacstrapExtraConf(t *testing.T) {
	cachyos := &PacstrapDef{ExtraRepos: []PacstrapRepo{
		{Name: "cachyos-v3", Server: "https://mirror.cachyos.org/repo/x86_64_v3/$repo", SigLevel: "Never"},
		{Name: "cachyos-core-v3", Server: "https://mirror.cachyos.org/repo/x86_64_v3/$repo", SigLevel: "Never"},
		{Name: "cachyos", Server: "https://mirror.cachyos.org/repo/$arch/$repo", SigLevel: "Never"},
	}}
	got := renderPacstrapExtraConf(cachyos)
	if !strings.Contains(got, "[options]\nArchitecture = x86_64 x86_64_v3\n") {
		t.Errorf("missing/incorrect Architecture directive for x86_64_v3 repos:\n%s", got)
	}
	if strings.Count(got, "SigLevel = Never") != 3 {
		t.Errorf("expected SigLevel emitted for all 3 repos, got:\n%s", got)
	}
	if strings.Count(got, "Architecture =") != 1 {
		t.Errorf("expected exactly one Architecture directive (deduped), got:\n%s", got)
	}

	// nil / empty → empty fragment (no spurious [options]).
	if s := renderPacstrapExtraConf(nil); s != "" {
		t.Errorf("nil PacstrapDef should render empty, got %q", s)
	}
	if s := renderPacstrapExtraConf(&PacstrapDef{}); s != "" {
		t.Errorf("no-repos PacstrapDef should render empty, got %q", s)
	}

	// Plain (non-microarch) repo without SigLevel → repo block, no [options].
	plain := &PacstrapDef{ExtraRepos: []PacstrapRepo{
		{Name: "extra", Server: "https://example.org/repo/$arch/$repo"},
	}}
	got = renderPacstrapExtraConf(plain)
	if strings.Contains(got, "[options]") {
		t.Errorf("plain repo should not emit [options]/Architecture, got:\n%s", got)
	}
	if !strings.Contains(got, "[extra]\nServer = https://example.org/repo/$arch/$repo\n") {
		t.Errorf("plain repo block missing, got:\n%s", got)
	}
	if strings.Contains(got, "SigLevel") {
		t.Errorf("no SigLevel set → none should be emitted, got:\n%s", got)
	}
}

// TestCachyosRuntimePacmanConf locks in the booted-guest /etc/pacman.conf the
// cachyos pacstrap writes into the rootfs. Single-source guard: runtime_pacman_conf
// is a TEMPLATE that derives its repo list from the one extra_repo source (no
// second hand-maintained copy), so the install + runtime configs cannot drift.
// Regression guard for the "config file /etc/pacman.conf could not be read"
// deploy failure AND for the cachyos-extra HTML-stub repo (must be absent from
// BOTH configs, by construction, now that extra_repo is the single source).
func TestCachyosRuntimePacmanConf(t *testing.T) {
	distroCfg, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	cachyos, ok := distroCfg.Distro["cachyos"]
	if !ok || cachyos.Pacstrap == nil {
		t.Fatal("cachyos distro / pacstrap missing from build.yml")
	}
	// Single source: the runtime config must DERIVE its repos from extra_repo
	// (template), not hardcode a second copy.
	if !strings.Contains(cachyos.Pacstrap.RuntimePacmanConf, ".ExtraRepos") {
		t.Errorf("runtime_pacman_conf must derive its repo list from extra_repo via {{ range .ExtraRepos }} (single source), got:\n%s", cachyos.Pacstrap.RuntimePacmanConf)
	}
	// Render it the way the bootstrap paths do.
	rc, err := renderRuntimePacmanConf(cachyos.Pacstrap)
	if err != nil {
		t.Fatalf("renderRuntimePacmanConf: %v", err)
	}
	if rc == "" {
		t.Fatal("rendered runtime_pacman_conf is empty — guests boot with no /etc/pacman.conf and add_layer pac installs fail")
	}
	for _, want := range []string{"[options]", "SigLevel = Never", "[cachyos-v3]", "[cachyos-core-v3]", "[cachyos]", "Include = /etc/pacman.d/mirrorlist"} {
		if !strings.Contains(rc, want) {
			t.Errorf("rendered runtime_pacman_conf missing %q:\n%s", want, rc)
		}
	}
	// cachyos-extra serves no usable DB (HTML stub). Removed from the single
	// extra_repo source, it must be absent from BOTH the rendered runtime config
	// AND the install config — the drift this cutover eliminated.
	if strings.Contains(rc, "cachyos-extra") {
		t.Errorf("runtime_pacman_conf must NOT include cachyos-extra:\n%s", rc)
	}
	if strings.Contains(renderPacstrapExtraConf(cachyos.Pacstrap), "cachyos-extra") {
		t.Errorf("install (extra_repo) config must NOT include cachyos-extra either — single source of truth")
	}
}
