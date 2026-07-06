package main

// check_synccreds_cmd.go — host-side `charly check sync-credential <score>`.
//
// One-shot copy of AI-CLI auth material from the host's $HOME into the
// score's target. Per-target dispatch:
//   - pod: `podman cp` into the running pod
//   - vm:  `scp` over SSH into the VM
//   - host: no-op (credentials already in the host's $HOME)

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunActual executes the credential sync. The CheckSyncCredCmd struct
// + its Kong tags are declared in check_runner_cmd.go.
func (c *CheckSyncCredCmd) RunActual() error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, ok, err := LoadUnified(projectDir)
	if err != nil {
		return err
	}
	if !ok || uf == nil {
		return errors.New("harness sync-credential: no charly.yml in current directory")
	}
	node, found := uf.Bundle[c.Score]
	if !found || node.Iterate == nil {
		return fmt.Errorf("harness sync-credential: entity %q has no iterate: block", c.Score)
	}
	iterate := node.Iterate
	tk, tn := ResolveIterateSandbox(uf, iterate.Sandbox)

	var aiNames []string
	if c.Agent != "" {
		aiNames = []string{c.Agent}
	} else {
		aiNames = iterate.Agent
	}
	if len(aiNames) == 0 {
		return fmt.Errorf("harness sync-credential: score %q has no agents configured", c.Score)
	}

	bodies := uf.PluginKinds["agent"]
	for _, aiName := range aiNames {
		ai, _, err := resolveAgentViaPlugin(bodies, aiName)
		if err != nil {
			return err
		}
		if len(ai.Credential) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "harness sync-credential: ai=%s where=%s:%s mounts=%d\n",
			aiName, tk, tn, len(ai.Credential))

		switch tk {
		case TargetKindPod:
			containerName := "charly-" + tn
			if err := podRunning(ctx, containerName); err != nil {
				return err
			}
			if err := syncCredentialsToPod(ctx, containerName, ai.Credential); err != nil {
				return fmt.Errorf("ai %s: %w", aiName, err)
			}
		case TargetKindVM:
			if err := syncCredentialsToVM(ctx, tn, ai.Credential); err != nil {
				return fmt.Errorf("ai %s (vm:%s): %w", aiName, tn, err)
			}
		case TargetKindHost:
			fmt.Fprintf(os.Stderr, "  (host target — no sync needed; credentials already in $HOME)\n")
		}
	}
	return nil
}

func podRunning(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "podman", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("harness: container %q not reachable: %w\n%s", containerName, err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("harness: container %q is not running — `charly start %s` first",
			containerName, strings.TrimPrefix(containerName, "charly-"))
	}
	return nil
}

func syncCredentialsToPod(ctx context.Context, containerName string, mounts []CredentialMount) error {
	var podHome string
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("credential src %q: %w", m.Src, err)
		}
		if _, err := os.Stat(srcAbs); err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "  src %q not found; skipping (optional)\n", m.Src)
				continue
			}
			return fmt.Errorf("credential src %q unreadable: %w", srcAbs, err)
		}
		dst := m.Dst
		if strings.HasPrefix(dst, "~") {
			if podHome == "" {
				h, err := resolveContainerHome(ctx, "podman", containerName)
				if err != nil {
					return fmt.Errorf("resolve pod $HOME for dst %q: %w", m.Dst, err)
				}
				podHome = h
			}
			dst = substTilde(dst, podHome)
		}
		parent := filepath.Dir(dst)
		if parent != "" && parent != "/" {
			mkCmd := exec.CommandContext(ctx, "podman", "exec", containerName, "mkdir", "-p", parent)
			if out, err := mkCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("mkdir -p %q in pod: %w\n%s", parent, err, string(out))
			}
		}
		cpCmd := exec.CommandContext(ctx, "podman", "cp", srcAbs, containerName+":"+dst)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("podman cp %q -> %q: %w\n%s", m.Src, dst, err, string(out))
		}
	}
	return nil
}

func syncCredentialsToVM(ctx context.Context, vmName string, mounts []CredentialMount) error {
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("credential src %q: %w", m.Src, err)
		}
		if _, err := os.Stat(srcAbs); err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "  src %q not found; skipping (optional)\n", m.Src)
				continue
			}
			return fmt.Errorf("credential src %q unreadable: %w", srcAbs, err)
		}
		// scpToVm (vm_scp.go) — the same host→guest single-file copy primitive
		// the `charly vm scp` subcommand uses (R3): resolves the managed
		// charly-<name> ssh alias, the guest $HOME for a leading ~ in dst, and
		// delivers the file USER-owned via SSHExecutor.PutFile.
		if err := scpToVm(ctx, vmName, srcAbs, m.Dst, m.Mode); err != nil {
			return fmt.Errorf("credential %q: %w", m.Src, err)
		}
	}
	return nil
}

func expandHostPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~ in %q: %w", p, err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func substTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

var resolveContainerHome = func(ctx context.Context, engine, container string) (string, error) {
	cmd := exec.CommandContext(ctx, engine, "exec", container,
		"sh", "-c", "getent passwd $(id -u) | cut -d: -f6")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getent passwd in %s: %w", container, err)
	}
	home := strings.TrimSpace(string(out))
	if home == "" {
		return "", fmt.Errorf("empty $HOME for container %s", container)
	}
	return home, nil
}
