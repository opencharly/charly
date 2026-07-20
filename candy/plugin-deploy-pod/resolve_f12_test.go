package deploypod

import (
	"reflect"
	"slices"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// resolve_f12_test.go — relocated 1:1 from the deleted charly-core shell_test.go's
// buildShellArgs/buildExecArgs argv-generation tests (the P13-KERNEL byte-diff gate: every
// hardcoded expected-argv assertion is UNCHANGED). The only translation: the former package-level
// isTerminal()/forceTTY mocking (withTerminal/withForceTTY) is replaced by an explicit `interactive`
// bool parameter — interactive = forceTTY || isTerminal(), computed per test case exactly as the
// former host-side decision was, since buildShellArgs/buildExecArgs now take the HOST-resolved
// value as data instead of reading global state (an out-of-process plugin's own stdio is not the
// operator's terminal). TestLocalizePort/TestResolveShellImageRef stayed in charly-core (those
// functions did not move).

func TestBuildShellArgs(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsCustomUIDGID(t *testing.T) {
	args := buildShellArgs("docker", "fedora:latest", 1001, 1002, nil, nil, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1001:1002",
		"--entrypoint", "bash",
		"fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithPorts(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, []string{"9090:9090", "8080:8080"}, nil, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithSinglePort(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, []string{"8080"}, nil, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithVolumes(t *testing.T) {
	volumes := []deploykit.VolumeMount{
		{VolumeName: "charly-openclaw-data", ContainerPath: "/home/user/.openclaw"},
	}
	args := buildShellArgs("docker", "ghcr.io/opencharly/openclaw:latest", 1000, 1000, nil, volumes, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-v", "charly-openclaw-data:/home/user/.openclaw",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/openclaw:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, nil, nil, true, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPUPodman(t *testing.T) {
	args := buildShellArgs("podman", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, nil, nil, true, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"podman", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--device", "nvidia.com/gpu=all",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithoutGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", false)
	for _, arg := range args {
		if arg == "--gpus" {
			t.Error("buildShellArgs(gpu=false) should not contain --gpus")
		}
	}
}

func TestBuildShellArgsWithCommand(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, nil, nil, nil, false, "echo hello", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", false)
	want := []string{
		"docker", "run", "--rm", "-i",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/fedora:latest",
		"-c", "echo hello",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithCommandAndGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, nil, nil, true, "nvidia-smi", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", false)
	want := []string{
		"docker", "run", "--rm", "-i",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/ollama:latest",
		"-c", "nvidia-smi",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgs(t *testing.T) {
	args := buildExecArgs("docker", "charly-fedora", 1000, 1000, "", nil, "/workspace", true)
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/workspace",
		"charly-fedora",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsWithCommand(t *testing.T) {
	args := buildExecArgs("docker", "charly-openclaw", 1000, 1000, "echo hello", nil, "/workspace", false)
	want := []string{
		"docker", "exec", "-i",
		"--user", "1000:1000",
		"-w", "/workspace",
		"charly-openclaw",
		"bash",
		"-c", "echo hello",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(command) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithCommandTTY(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, nil, nil, nil, false, "openclaw tui", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/opencharly/fedora:latest",
		"-c", "openclaw tui",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command+tty) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsWithCommandTTY(t *testing.T) {
	args := buildExecArgs("docker", "charly-openclaw", 1000, 1000, "openclaw tui", nil, "/workspace", true)
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/workspace",
		"charly-openclaw",
		"bash",
		"-c", "openclaw tui",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(command+tty) =\n  %v\nwant\n  %v", args, want)
	}
}

// TestBuildShellArgsForceTTY — was withTerminal(false)+withForceTTY(true): forced --tty without a
// real terminal, interactive = force||isTerminal = true||false = true (the exact scenario
// hostAttachScript's wrapPTY additionally handles by wrapping in script(1) — tested separately).
func TestBuildShellArgsForceTTY(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, nil, nil, nil, false, "openclaw models auth login", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash", "ghcr.io/opencharly/fedora:latest",
		"-c", "openclaw models auth login",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(forceTTY) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsForceTTY(t *testing.T) {
	args := buildExecArgs("docker", "charly-openclaw", 1000, 1000, "openclaw models auth login", nil, "/workspace", true)
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/workspace",
		"charly-openclaw",
		"bash",
		"-c", "openclaw models auth login",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(forceTTY) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsCustomUIDGID(t *testing.T) {
	args := buildExecArgs("podman", "charly-ubuntu", 1001, 1002, "", nil, "/workspace", true)
	want := []string{
		"podman", "exec", "-it",
		"--user", "1001:1002",
		"-w", "/workspace",
		"charly-ubuntu",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(custom uid/gid) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithEnvVars(t *testing.T) {
	envVars := []string{"DB_HOST=localhost", "SECRET=abc"}
	args := buildShellArgs("docker", "myapp:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", envVars, spec.SecurityConfig{}, "/workspace", true)

	entryIdx := -1
	envIdx := -1
	for i, arg := range args {
		if arg == "-e" && envIdx == -1 {
			envIdx = i
		}
		if arg == "--entrypoint" {
			entryIdx = i
		}
	}
	if envIdx < 0 {
		t.Fatal("expected -e flags in args")
	}
	if entryIdx < envIdx {
		t.Error("expected -e flags before --entrypoint")
	}

	found := 0
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) {
			if args[i+1] == "DB_HOST=localhost" || args[i+1] == "SECRET=abc" {
				found++
			}
		}
	}
	if found != 2 {
		t.Errorf("expected 2 env vars, found %d in args: %v", found, args)
	}
}

func TestBuildShellArgsWithBindMounts(t *testing.T) {
	bindMounts := []deploykit.ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("docker", "myapp:latest", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)

	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/home/user/data:/home/user/.myapp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v /home/user/data:/home/user/.myapp in args, got: %v", args)
	}
	// Docker should NOT have --userns
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			t.Error("docker should not have --userns=keep-id")
		}
	}
}

func TestBuildShellArgsWithBindMountsPodman(t *testing.T) {
	bindMounts := []deploykit.ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("podman", "myapp:latest", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, spec.SecurityConfig{}, "/workspace", true)

	found := slices.Contains(args, "--userns=keep-id:uid=1000,gid=1000")
	if !found {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 in podman args, got: %v", args)
	}
}

func TestBuildShellArgsWithCapAdd(t *testing.T) {
	sec := spec.SecurityConfig{
		CapAdd:  []string{"SYS_ADMIN"},
		Devices: []string{"/dev/fuse"},
	}
	args := buildShellArgs("docker", "myimage:latest", 0, 0, nil, nil, nil, false, "", "127.0.0.1", nil, sec, "/workspace", true)
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

func TestBuildExecArgsWithEnvVars(t *testing.T) {
	envVars := []string{"FOO=bar"}
	args := buildExecArgs("docker", "charly-myapp", 1000, 1000, "", envVars, "/workspace", true)
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/workspace",
		"-e", "FOO=bar",
		"charly-myapp",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(envVars) =\n  %v\nwant\n  %v", args, want)
	}
}
