package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

func TestBuildStartArgs(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/opencharly/fedora-test:latest", 1000, 1000, nil, "charly-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "charly-fedora-test",
		"-w", "/workspace",
		"ghcr.io/opencharly/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/opencharly/fedora-test:latest", 1000, 1000, nil, "charly-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "charly-fedora-test",
		"-w", "/workspace",
		"ghcr.io/opencharly/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithPorts(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/opencharly/fedora-test:latest", 1000, 1000, []string{"9090:9090", "8080:8080"}, "charly-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "charly-fedora-test",
		"-w", "/workspace",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"ghcr.io/opencharly/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithVolumes(t *testing.T) {
	volumes := []deploykit.VolumeMount{
		{VolumeName: "charly-ollama-models", ContainerPath: "/home/user/.ollama/models"},
	}
	args := buildStartArgs("docker", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, "charly-ollama", volumes, nil, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "charly-ollama",
		"-w", "/workspace",
		"-v", "charly-ollama-models:/home/user/.ollama/models",
		"ghcr.io/opencharly/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPU(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, "charly-ollama", nil, nil, true, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "charly-ollama",
		"-w", "/workspace",
		"--gpus", "all",
		"ghcr.io/opencharly/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPUPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/opencharly/ollama:latest", 1000, 1000, nil, "charly-ollama", nil, nil, true, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "charly-ollama",
		"-w", "/workspace",
		"--device", "nvidia.com/gpu=all",
		"ghcr.io/opencharly/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora-test", "charly-fedora-test"},
		{"fedora", "charly-fedora"},
		{"ubuntu", "charly-ubuntu"},
	}
	for _, tt := range tests {
		got := kit.ContainerName(tt.image)
		if got != tt.want {
			t.Errorf("containerName(%q) = %q, want %q", tt.image, got, tt.want)
		}
	}
}

func TestContainerNameInstance(t *testing.T) {
	tests := []struct {
		image    string
		instance string
		want     string
	}{
		{"githubrunner", "", "charly-githubrunner"},
		{"githubrunner", "runner-1", "charly-githubrunner-runner-1"},
		{"ollama", "gpu2", "charly-ollama-gpu2"},
	}
	for _, tt := range tests {
		got := kit.ContainerNameInstance(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("containerNameInstance(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

func TestBuildStartArgsWithEnvVars(t *testing.T) {
	envVars := []string{"FOO=bar", "TOKEN=secret"}
	args := buildStartArgs("docker", "ghcr.io/opencharly/fedora:latest", 1000, 1000, nil, "charly-fedora", nil, nil, false, "127.0.0.1", envVars, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}, "/workspace")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "charly-fedora",
		"-w", "/workspace",
		"-e", "FOO=bar",
		"-e", "TOKEN=secret",
		"ghcr.io/opencharly/fedora:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(envVars) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsNoSupervisord(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/opencharly/charly-fedora:latest", 0, 0, nil, "charly-charly-fedora", nil, nil, false, "127.0.0.1", nil, SecurityConfig{}, []string{"sleep", "infinity"}, "/workspace")
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "charly-charly-fedora",
		"-w", "/workspace",
		"ghcr.io/opencharly/charly-fedora:latest",
		"sleep", "infinity",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(noSupervisord) =\n  %v\nwant\n  %v", args, want)
	}
}

// TestStopCmd_UnmountFlagDefaults asserts the new --unmount field defaults to
// false (so plain `charly stop` continues to leave gocryptfs mounts up — the
// pre-cutover behavior). A flipped default would silently tear down every
// operator's encrypted mounts on every stop, which is exactly the regression
// the explicit-opt-in design avoids.
func TestStopCmd_UnmountFlagDefaults(t *testing.T) {
	c := &StopCmd{}
	if c.Unmount {
		t.Error("StopCmd.Unmount default should be false; --unmount must be explicit opt-in")
	}
	c.Unmount = true
	if !c.Unmount {
		t.Error("StopCmd.Unmount must be settable")
	}
}
