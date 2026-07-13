package check

// synccreds.go — `charly check sync-credential <score>` (P12: relocated from
// charly/check_synccreds_cmd.go).
//
// One-shot copy of AI-CLI auth material from the host's $HOME into the score's
// target. Per-target dispatch: pod → `podman cp` (plugin-local); vm → `charly vm scp`
// (the "cli" host seam, the shared host→guest single-file copy primitive); host →
// no-op (credentials already in the host's $HOME). The project read (iterate block +
// sandbox class + agent catalog) is derived off the resolved-project envelope; agent
// resolution rides InvokeProvider(kind:agent).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// CheckSyncCredCmd is `charly check sync-credential <score>`.
type CheckSyncCredCmd struct {
	Score string `arg:"" help:"Score name"`
	Agent string `name:"agent" help:"Sync credentials for this agent only (default: all configured)"`
}

func (c *CheckSyncCredCmd) Run() error { return c.RunActual() }

// RunActual executes the credential sync.
func (c *CheckSyncCredCmd) RunActual() error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	reply, err := resolveCheckProjection(cmdExec, cmdCtx, c.Score, projectDir)
	if err != nil {
		return err
	}
	if !reply.HasNode {
		return errors.New("harness sync-credential: no charly.yml in current directory")
	}
	if !reply.HasIterate {
		return fmt.Errorf("harness sync-credential: entity %q has no iterate: block", c.Score)
	}
	var iterate spec.Iterate
	if len(reply.IterateJSON) > 0 {
		if err := json.Unmarshal(reply.IterateJSON, &iterate); err != nil {
			return fmt.Errorf("harness sync-credential: decode iterate: %w", err)
		}
	}
	tk, tn := reply.SandboxKind, reply.SandboxName

	var aiNames []string
	if c.Agent != "" {
		aiNames = []string{c.Agent}
	} else {
		aiNames = iterate.Agent
	}
	if len(aiNames) == 0 {
		return fmt.Errorf("harness sync-credential: score %q has no agents configured", c.Score)
	}

	bodies := reply.AgentBodies
	for _, aiName := range aiNames {
		ai, err := resolveAgentSpec(cmdExec, cmdCtx, bodies, aiName)
		if err != nil {
			return err
		}
		if len(ai.Credential) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "harness sync-credential: ai=%s where=%s:%s mounts=%d\n",
			aiName, tk, tn, len(ai.Credential))

		switch tk {
		case targetKindPod:
			containerName := "charly-" + tn
			if err := podRunning(ctx, containerName); err != nil {
				return err
			}
			if err := syncCredentialsToPod(ctx, containerName, ai.Credential); err != nil {
				return fmt.Errorf("ai %s: %w", aiName, err)
			}
		case targetKindVM:
			if err := syncCredentialsToVM(ctx, tn, ai.Credential); err != nil {
				return fmt.Errorf("ai %s (vm:%s): %w", aiName, tn, err)
			}
		case targetKindHost:
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

func syncCredentialsToPod(ctx context.Context, containerName string, mounts []spec.CredentialMount) error {
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

// syncCredentialsToVM copies each credential mount into the VM sandbox via the
// shared `charly vm scp` host→guest single-file primitive (the "cli" host seam). The
// command resolves the guest endpoint + a leading ~ in dst against the guest $HOME +
// USER-owned delivery itself; the optional-source skip is preserved plugin-side (a
// missing optional src is not shipped). The per-mount `mode:` is not threaded (the
// `charly vm scp` command uses the source's mode) — a benign divergence for
// credential files.
func syncCredentialsToVM(ctx context.Context, vmName string, mounts []spec.CredentialMount) error {
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
		reply, err := bedCli(cmdExec, ctx, false, "vm", "scp", vmName, srcAbs, m.Dst)
		if err != nil {
			return fmt.Errorf("credential %q: %w", m.Src, err)
		}
		if reply.ExitCode != 0 {
			return fmt.Errorf("credential %q: vm scp exited %d: %s", m.Src, reply.ExitCode, reply.Error)
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
