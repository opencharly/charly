package main

import (
	"reflect"
	"slices"
	"testing"
)

func TestSecurityArgsPrivileged(t *testing.T) {
	args := SecurityArgs(SecurityConfig{Privileged: true})
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged) = %v, want %v", args, want)
	}
}

func TestSecurityArgsCapabilities(t *testing.T) {
	args := SecurityArgs(SecurityConfig{
		CapAdd:      []string{"SYS_ADMIN", "MKNOD"},
		Devices:     []string{"/dev/fuse"},
		SecurityOpt: []string{"label=disable"},
	})
	want := []string{
		"--cap-add", "SYS_ADMIN",
		"--cap-add", "MKNOD",
		"--device", "/dev/fuse",
		"--security-opt", "label=disable",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(caps) = %v, want %v", args, want)
	}
}

func TestSecurityArgsEmpty(t *testing.T) {
	args := SecurityArgs(SecurityConfig{})
	if len(args) != 0 {
		t.Errorf("SecurityArgs(empty) = %v, want empty", args)
	}
}

func TestSecurityArgsPrivilegedOverridesCaps(t *testing.T) {
	// When privileged is true, only --privileged is emitted (caps are redundant)
	args := SecurityArgs(SecurityConfig{
		Privileged: true,
		CapAdd:     []string{"SYS_ADMIN"},
	})
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged+caps) = %v, want %v", args, want)
	}
}

func TestAppendUnique(t *testing.T) {
	result := appendUnique([]string{"a", "b"}, "b", "c", "a", "d")
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("appendUnique = %v, want %v", result, want)
	}
}

func TestSecurityArgsShmSize(t *testing.T) {
	args := SecurityArgs(SecurityConfig{ShmSize: "1g"})
	want := []string{"--shm-size", "1g"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(shm_size) = %v, want %v", args, want)
	}
}

func TestSecurityArgsShmSizeWithPrivileged(t *testing.T) {
	args := SecurityArgs(SecurityConfig{Privileged: true, ShmSize: "512m"})
	want := []string{"--privileged", "--shm-size", "512m"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged+shm) = %v, want %v", args, want)
	}
}

// TestSecurityArgsShmSizeDroppedWhenIpcHost covers the 2026-04-27 fix
// for the shm_size + ipc=host conflict: when IpcMode is "host", the
// kernel-level /dev/shm is shared with the host and podman REJECTS
// `--shm-size` with a runtime error. The fix drops the flag.
func TestSecurityArgsShmSizeDroppedWhenIpcHost(t *testing.T) {
	args := SecurityArgs(SecurityConfig{ShmSize: "1g", IpcMode: "host"})
	for _, a := range args {
		if a == "--shm-size" {
			t.Errorf("expected no `--shm-size` flag when IpcMode=host; got %v", args)
		}
	}
	// `--ipc host` MUST be present.
	foundIpc := false
	for i, a := range args {
		if a == "--ipc" && i+1 < len(args) && args[i+1] == "host" {
			foundIpc = true
		}
	}
	if !foundIpc {
		t.Errorf("expected `--ipc host` flag; got %v", args)
	}
}

// TestSecurityArgsShmSizeRetainedWhenIpcPrivate verifies the gate
// only fires for IpcMode=host — other values (private, shareable,
// empty) keep the flag.
func TestSecurityArgsShmSizeRetainedWhenIpcPrivate(t *testing.T) {
	args := SecurityArgs(SecurityConfig{ShmSize: "1g", IpcMode: "private"})
	foundShm := false
	for i, a := range args {
		if a == "--shm-size" && i+1 < len(args) && args[i+1] == "1g" {
			foundShm = true
		}
	}
	if !foundShm {
		t.Errorf("expected `--shm-size 1g` flag with IpcMode=private; got %v", args)
	}
}

// TestIpcModeBlocksShmSize is the helper-level check for the gate.
// Only "host" should trigger the drop.
func TestIpcModeBlocksShmSize(t *testing.T) {
	cases := []struct {
		ipc  string
		want bool
	}{
		{"host", true},
		{"private", false},
		{"shareable", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := ipcModeBlocksShmSize(tc.ipc); got != tc.want {
			t.Errorf("ipcModeBlocksShmSize(%q) = %v, want %v", tc.ipc, got, tc.want)
		}
	}
}

func TestMaxShmSize(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"", "1g", "1g"},
		{"1g", "", "1g"},
		{"256m", "1g", "1g"},
		{"2g", "1g", "2g"},
		{"512m", "512m", "512m"},
	}
	for _, tt := range tests {
		got := maxShmSize(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("maxShmSize(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseShmBytes(t *testing.T) {
	tests := []struct {
		s    string
		want int64
	}{
		{"1g", 1024 * 1024 * 1024},
		{"256m", 256 * 1024 * 1024},
		{"64k", 64 * 1024},
		{"1024", 1024},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseShmBytes(tt.s)
		if got != tt.want {
			t.Errorf("parseShmBytes(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestMinCap(t *testing.T) {
	// Smallest-wins: opposite of maxShmSize. Tighter cap is safer.
	tests := []struct {
		a, b, want string
	}{
		{"", "1g", "1g"},
		{"1g", "", "1g"},
		{"256m", "1g", "256m"},
		{"2g", "1g", "1g"},
		{"512m", "512m", "512m"},
		{"1024m", "1g", "1024m"}, // equal sizes — first wins
	}
	for _, tt := range tests {
		got := minCap(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minCap(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMinCpus(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"", "2", "2"},
		{"2", "", "2"},
		{"1.5", "4", "1.5"},
		{"8", "2.5", "2.5"},
		{"2", "2", "2"},
		{"bogus", "2", "2"}, // unparseable → other wins
		{"2", "bogus", "2"},
	}
	for _, tt := range tests {
		got := minCpus(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minCpus(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSecurityArgsMemoryCaps(t *testing.T) {
	args := SecurityArgs(SecurityConfig{
		MemoryMax:     "6g",
		MemoryHigh:    "5g",
		MemorySwapMax: "2g",
		Cpus:          "4",
	})
	want := []string{
		"--memory", "6g",
		"--memory-reservation", "5g",
		"--memory-swap", "2g",
		"--cpus", "4",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(caps) = %v, want %v", args, want)
	}
}

func TestSecurityArgsMemoryCapsWithPrivileged(t *testing.T) {
	// Privileged containers still need resource caps — they can run anything
	// kernel-level but don't get a free pass on memory/CPU.
	args := SecurityArgs(SecurityConfig{
		Privileged: true,
		ShmSize:    "1g",
		MemoryMax:  "6g",
		Cpus:       "2.5",
	})
	want := []string{
		"--privileged",
		"--shm-size", "1g",
		"--memory", "6g",
		"--cpus", "2.5",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged+caps) = %v, want %v", args, want)
	}
}

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
		Box: boxMapOf(map[string]BoxConfig{
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
		Box: boxMapOf(map[string]BoxConfig{
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
	cfg := QuadletConfig{
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
	content := generateQuadlet(cfg)
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

func TestNormalizeCgroupSize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"6g", "6G"},
		{"512m", "512M"},
		{"64k", "64K"},
		{"1t", "1T"},
		{"6G", "6G"}, // already uppercase
		{"512M", "512M"},
		{"1024", "1024"}, // raw bytes, no suffix
		{"  2g  ", "2G"},
	}
	for _, tt := range tests {
		got := normalizeCgroupSize(tt.in)
		if got != tt.want {
			t.Errorf("normalizeCgroupSize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatCPUQuota(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"0", ""},
		{"-1", ""},
		{"bogus", ""},
		{"1", "100%"},
		{"2.5", "250%"},
		{"0.5", "50%"},
		{"4", "400%"},
		{"  2  ", "200%"},
	}
	for _, tt := range tests {
		got := formatCPUQuota(tt.in)
		if got != tt.want {
			t.Errorf("formatCPUQuota(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

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
	cfg := QuadletConfig{
		BoxName:  "runner",
		ImageRef: "ghcr.io/test/runner:latest",
		Home:     "/workspace",
		Security: SecurityConfig{Privileged: true},
	}
	content := generateQuadlet(cfg)
	if !containsLine(content, "PodmanArgs=--privileged") {
		t.Error("expected PodmanArgs=--privileged in quadlet")
	}
	if !containsLine(content, "SecurityLabelDisable=true") {
		t.Error("expected SecurityLabelDisable=true in quadlet")
	}
}

func TestGenerateQuadletWithCapAdd(t *testing.T) {
	cfg := QuadletConfig{
		BoxName:  "builder",
		ImageRef: "ghcr.io/test/builder:latest",
		Home:     "/workspace",
		Security: SecurityConfig{
			CapAdd:      []string{"SYS_ADMIN"},
			Devices:     []string{"/dev/fuse"},
			SecurityOpt: []string{"label=disable"},
		},
	}
	content := generateQuadlet(cfg)
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
