package main

// layer_artifacts.go — retrieves files declared in a candy's `artifacts:`
// block after the candy's setup has completed on the deploy target.
//
// Retricheck uses the DeployExecutor's GetFile back-channel (os.ReadFile
// on host, `ssh vm sudo cat` on VM, `podman exec cat` via the nested
// executor on container-in-container cases). Rewrite rules apply
// literal find/replace against the retrieved content before writing to
// the operator-side destination. Missing-file handling depends on the
// artifact's `optional:` flag.
//
// Called from charly/deploy_add_cmd.go after target.Emit succeeds and any
// deploy-scope tests pass — this is the finalization step that ends a
// successful `charly bundle add`.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk/spec"
)

// RetrieveCandyArtifacts walks every artifact declared by every candy
// included in the deploy and pulls it back via the executor's GetFile.
// Missing non-optional files are a hard error (R1).
//
// deployName is the deploy-yml name (e.g., "vm:k3s-srv") — exposed to
// rewrite-path expansion as ${deploy_name}. envVars is an additional
// substitution context (e.g., K3S_SERVER_HOSTNAME from the deploy.env
// block, used to rewrite server URLs in a retrieved kubeconfig).
func RetrieveCandyArtifacts(
	ctx context.Context,
	exec spec.DeployExecutor,
	layers []spec.CandyReader,
	deployName string,
	envVars map[string]string,
	opts spec.EmitOpts,
) error {
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		artifacts := layer.Artifact()
		if len(artifacts) == 0 {
			continue
		}
		for _, a := range artifacts {
			if err := retrieveOne(ctx, exec, layer.GetName(), a, deployName, envVars, opts); err != nil {
				return fmt.Errorf("candy %q artifact %q: %w", layer.GetName(), a.Name, err)
			}
		}
	}
	return nil
}

// retrieveOne handles a single artifact.
func retrieveOne(
	ctx context.Context,
	exec spec.DeployExecutor,
	candyName string,
	a CandyArtifact,
	deployName string,
	envVars map[string]string,
	opts spec.EmitOpts,
) error {
	if a.Path == "" || a.RetrieveTo == "" {
		return fmt.Errorf("invalid artifact declaration (path and retrieve_to are required)")
	}

	// Optional readiness wait — for artifacts written by a service that
	// reaches "active" BEFORE its output file lands (canonical case:
	// k3s.service writes /etc/rancher/k3s/k3s.yaml ~3-15s after the
	// systemd unit transitions to active). Polls every 1s until the
	// file exists or the deadline elapses. File existence IS the
	// synchronization primitive — this is a readiness probe, not a
	// sleep workaround (R4).
	if a.WaitSeconds > 0 {
		if err := waitForArtifactPath(ctx, exec, a.Path, time.Duration(a.WaitSeconds)*time.Second, opts); err != nil {
			if a.Optional && isMissingFile(err) {
				return nil
			}
			return fmt.Errorf("waiting for %s: %w", a.Path, err)
		}
	}

	// GetFile with asRoot=true — artifacts are typically system-owned
	// files (kubeconfig, service state) that require sudo to read on the
	// target. Candies that need a user-owned file can add a future
	// `as_user:` flag; the current schema is deliberately narrow.
	data, err := exec.GetFile(ctx, a.Path, true /*asRoot*/, opts)
	if err != nil {
		if a.Optional && isMissingFile(err) {
			return nil
		}
		return fmt.Errorf("retrieving %s: %w", a.Path, err)
	}

	// Dry-run GetFile returns nil data — skip write.
	if data == nil && opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] would retrieve %s -> %s\n", a.Path, a.RetrieveTo)
		return nil
	}

	// Apply rewrite rules in declared order.
	content := string(data)
	for _, r := range a.Rewrite {
		if r.Find == "" {
			continue
		}
		find := expandArtifactVars(r.Find, deployName, candyName, envVars)
		replace := expandArtifactVars(r.Replace, deployName, candyName, envVars)
		content = strings.ReplaceAll(content, find, replace)
	}

	// Expand ${...} in retrieve_to (most useful: ${deploy_name}).
	destPath := expandArtifactVars(a.RetrieveTo, deployName, candyName, envVars)
	destPath, err = expandArtifactHome(destPath)
	if err != nil {
		return err
	}

	mode := parseArtifactMode(a.Mode)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(destPath), err)
	}
	if err := os.WriteFile(destPath, []byte(content), mode); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	fmt.Fprintf(os.Stderr, "retrieved artifact %s -> %s\n", a.Path, destPath)
	return nil
}

// waitForArtifactPath polls exec.GetFile every 1s until the artifact
// path exists or the deadline elapses. Returns nil on success, a
// missing-file error on timeout, or any non-missing error from
// GetFile (auth failure, network partition) immediately.
//
// Used by retrieveOne for artifacts with WaitSeconds > 0. The file's
// existence is the synchronization primitive — polling cadence is the
// 1s sleep, not a fixed-duration wait. Honors context cancellation so
// dispatcher-level timeouts win over the per-artifact deadline.
func waitForArtifactPath(
	ctx context.Context,
	exec spec.DeployExecutor,
	path string,
	maxWait time.Duration,
	opts spec.EmitOpts,
) error {
	if opts.DryRun {
		return nil
	}
	// AUTHOR/CALLER cap (poll.go WaitCapped, NoProgress disabled): maxWait is a
	// per-artifact contract, preserved EXACTLY (not load-robustified). FATAL on a
	// non-missing-file error (auth/network/permission won't self-heal); keep
	// waiting on missing-file. Per-attempt context bounds a hung GetFile.
	cfg := loadedReadiness().WaitCapped("artifact "+path, PollRemote, maxWait)
	var fatalErr, lastErr error
	pErr := pollUntil(ctx, cfg, func(actx context.Context) (bool, float64, error) {
		_, gerr := exec.GetFile(actx, path, true /*asRoot*/, opts)
		if gerr == nil {
			return true, 0, nil
		}
		if !isMissingFile(gerr) {
			fatalErr = gerr
			return false, 0, ErrPollFatal // fail fast
		}
		lastErr = gerr
		return false, 0, nil // missing — keep waiting
	})
	if fatalErr != nil {
		return fatalErr
	}
	if pErr != nil {
		return fmt.Errorf("timeout after %s waiting for %s: %w", maxWait, path, lastErr)
	}
	return nil
}

// expandArtifactVars resolves ${deploy_name}, ${layer_name}, ${HOME},
// and any caller-supplied env vars. Unknown references are left as-is
// — literal text that happens to look like a variable reference should
// not silently empty-string out.
//
// Supports shell-style ${KEY:-default} fallback: when KEY is unset or
// empty, the substitution resolves to `default`. Needed for candy
// artifact rewrites that want a sensible fallback when the operator
// doesn't set an optional env var (e.g. K3S_KUBECONFIG_SERVER).
// Nested ${} is NOT supported — keep defaults literal.
func expandArtifactVars(s, deployName, candyName string, envVars map[string]string) string {
	mapFn := func(key string) string {
		// ${KEY:-default} fallback syntax.
		var defaultVal string
		if idx := strings.Index(key, ":-"); idx >= 0 {
			defaultVal = key[idx+2:]
			key = key[:idx]
		}
		resolved := ""
		switch key {
		case "deploy_name":
			resolved = deployName
		case "layer_name":
			resolved = candyName
		case "HOME":
			if home, err := os.UserHomeDir(); err == nil {
				resolved = home
			} else {
				resolved = os.Getenv("HOME")
			}
		default:
			if v, ok := envVars[key]; ok {
				resolved = v
			} else if v := os.Getenv(key); v != "" {
				resolved = v
			}
		}
		if resolved != "" {
			return resolved
		}
		if defaultVal != "" {
			return defaultVal
		}
		// Unknown ref with no default — leave intact.
		return "${" + key + "}"
	}
	return os.Expand(s, mapFn)
}

// expandArtifactHome expands a leading "~" to the user's home directory. Filepath
// joins don't honor "~"; this is the explicit step.
func expandArtifactHome(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, fmt.Errorf("resolving ~: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// parseArtifactMode turns an octal mode string ("0644") into an fs.FileMode.
// Empty or malformed defaults to 0644.
func parseArtifactMode(s string) fs.FileMode {
	if s == "" {
		return 0o644
	}
	if n, err := strconv.ParseUint(s, 8, 32); err == nil {
		return fs.FileMode(n)
	}
	return 0o644
}

// isMissingFile heuristically classifies an error as "file does not
// exist". Used to honor `optional: true` on artifacts. Checks both
// os.IsNotExist (local path) and common SSH-cat stderr patterns for
// remote targets.
func isMissingFile(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "No such file or directory") ||
		strings.Contains(msg, "cannot access") ||
		strings.Contains(msg, "not found")
}
