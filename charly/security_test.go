package main

import (
	"slices"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

func TestCollectSecurityMergesCapsSmallest(t *testing.T) {
	// Two candies disagreeing on memory_max — tightest wins.
	layers := map[string]*Candy{
		"big": {
			security: &SecurityConfig{MemoryMax: "8g", MemoryHigh: "7g", Cpus: "8"},
		},
		"small": {
			security: &SecurityConfig{MemoryMax: "4g", MemoryHigh: "3g", Cpus: "2"},
		},
	}
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"test": {Candy: []string{"big", "small"}},
		}),
	}
	sec := CollectSecurity(cfg, layers, "test")
	if sec.MemoryMax != "4g" {
		t.Errorf("MemoryMax = %q, want 4g (smallest wins)", sec.MemoryMax)
	}
	if sec.MemoryHigh != "3g" {
		t.Errorf("MemoryHigh = %q, want 3g", sec.MemoryHigh)
	}
	if sec.Cpus != "2" {
		t.Errorf("Cpus = %q, want 2", sec.Cpus)
	}
}

func TestCollectSecurityImageOverridesCaps(t *testing.T) {
	// Box-level security.memory_max replaces whatever the candies decided,
	// consistent with how ShmSize is handled.
	layers := map[string]*Candy{
		"chrome": {
			security: &SecurityConfig{MemoryMax: "6g", ShmSize: "1g"},
		},
	}
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"heavy": {
				Candy:    []string{"chrome"},
				Security: &SecurityConfig{MemoryMax: "16g"},
			},
		}),
	}
	sec := CollectSecurity(cfg, layers, "heavy")
	if sec.MemoryMax != "16g" {
		t.Errorf("MemoryMax = %q, want 16g (box override)", sec.MemoryMax)
	}
	if sec.ShmSize != "1g" {
		t.Errorf("ShmSize = %q, want 1g (candy default preserved)", sec.ShmSize)
	}
}

func TestGenerateQuadletWithMemoryCaps(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:  "selkies-desktop",
		ImageRef: "ghcr.io/test/selkies-desktop:latest",
		Home:     "/home/user",
		Security: SecurityConfig{
			ShmSize:       "1g",
			MemoryMax:     "6g",
			MemoryHigh:    "5g",
			MemorySwapMax: "2g",
			Cpus:          "4",
		},
	}
	content := deploykit.GenerateQuadlet(cfg)
	// systemd rejects lowercase size suffixes on MemoryMax/MemoryHigh/
	// MemorySwapMax (silently falls back to infinity). ShmSize is podman's
	// own flag and keeps its original lowercase form.
	for _, want := range []string{
		"ShmSize=1g",
		"MemoryMax=6G",
		"MemoryHigh=5G",
		"MemorySwapMax=2G",
		"CPUQuota=400%",
	} {
		if !containsLine(content, want) {
			t.Errorf("expected %q in quadlet:\n%s", want, content)
		}
	}
}

// TestNormalizeCgroupSize + TestFormatCPUQuota moved to sdk/deploykit
// (quadlet_test.go) with the NormalizeCgroupSize/FormatCPUQuota helpers in P11.
// TestSecurityArgs* + TestAppendUnique/TestIpcModeBlocksShmSize/TestMaxShmSize/
// TestParseShmBytes/TestMinCap/TestMinCpus moved to sdk/deploykit/security_test.go
// with the CollectSecurity split (W9) — SecurityArgs/ResourceCapArgs/AppendUniqueString/
// the byte-size helpers now live there exclusively.

func TestBuildStartArgsWithPrivileged(t *testing.T) {
	sec := SecurityConfig{Privileged: true}
	args := buildStartArgs("docker", "myimage:latest", 0, 0, nil, "charly-myimage", nil, nil, false, "127.0.0.1", nil, sec, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	found := slices.Contains(args, "--privileged")
	if !found {
		t.Errorf("expected --privileged in args: %v", args)
	}
}

func TestBuildShellArgsWithCapAdd(t *testing.T) {
	withTerminal(t, true)
	sec := SecurityConfig{
		CapAdd:  []string{"SYS_ADMIN"},
		Devices: []string{"/dev/fuse"},
	}
	args := buildShellArgs("docker", "myimage:latest", 0, 0, nil, nil, nil, false, "", "127.0.0.1", nil, sec, "/workspace")
	foundCap := false
	foundDev := false
	for i, arg := range args {
		if arg == "--cap-add" && i+1 < len(args) && args[i+1] == "SYS_ADMIN" {
			foundCap = true
		}
		if arg == "--device" && i+1 < len(args) && args[i+1] == "/dev/fuse" {
			foundDev = true
		}
	}
	if !foundCap {
		t.Errorf("expected --cap-add SYS_ADMIN in args: %v", args)
	}
	if !foundDev {
		t.Errorf("expected --device /dev/fuse in args: %v", args)
	}
}

func TestGenerateQuadletWithPrivileged(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:  "runner",
		ImageRef: "ghcr.io/test/runner:latest",
		Home:     "/workspace",
		Security: SecurityConfig{Privileged: true},
	}
	content := deploykit.GenerateQuadlet(cfg)
	if !containsLine(content, "PodmanArgs=--privileged") {
		t.Error("expected PodmanArgs=--privileged in quadlet")
	}
	if !containsLine(content, "SecurityLabelDisable=true") {
		t.Error("expected SecurityLabelDisable=true in quadlet")
	}
}

func TestGenerateQuadletWithCapAdd(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:  "builder",
		ImageRef: "ghcr.io/test/builder:latest",
		Home:     "/workspace",
		Security: SecurityConfig{
			CapAdd:      []string{"SYS_ADMIN"},
			Devices:     []string{"/dev/fuse"},
			SecurityOpt: []string{"label=disable"},
		},
	}
	content := deploykit.GenerateQuadlet(cfg)
	if !containsLine(content, "AddCapability=SYS_ADMIN") {
		t.Errorf("expected AddCapability=SYS_ADMIN in quadlet:\n%s", content)
	}
	if !containsLine(content, "AddDevice=/dev/fuse") {
		t.Errorf("expected AddDevice=/dev/fuse in quadlet:\n%s", content)
	}
	if !containsLine(content, "SecurityLabelDisable=true") {
		t.Errorf("expected SecurityLabelDisable=true in quadlet:\n%s", content)
	}
}

func containsLine(content, line string) bool {
	return slices.Contains(splitLines(content), line)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
