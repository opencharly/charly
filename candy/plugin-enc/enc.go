// Package enc is the ENCRYPTED-VOLUME (gocryptfs) MECHANICS plugin (C16a): it runs
// the gocryptfs / systemd-run / fusermount3 shell mechanics that mount, unmount,
// initialize, and re-key charly's gocryptfs-backed encrypted volumes. It is the
// security-sensitive external-command surface carved out of charly core
// (charly/enc.go).
//
// charly's core keeps the DEPLOY-MODEL that surrounds gocryptfs — ResolvedBindMount
// / ResolveVolumeBacking, the config loader (loadEncryptedVolume), the path/probe
// helpers (encryptedPlainDir / isEncryptedMounted / isEncryptedInitialized /
// cipherPopulatedPlainEmpty, which the mandatorily-core ResolveVolumeBacking +
// verifyBindMounts consume synchronously), and the credential store — and its
// in-core enc shim HOST-PRELIFTS all of it into a self-contained spec.EncExecInput
// (the resolved per-volume plan + the resolved passphrase) that this plugin
// executes. So this plugin owns ONLY "how to drive gocryptfs", nothing about
// charly's path conventions, state detection, config loading, or credentials.
//
// Compiled-in (charly/charly.yml compiled_plugins) — charly config mount/unmount/
// passwd + charly start's ensure hook all call the in-core shim, which resolves
// verb:enc and Invokes OpExecute in-proc (an inprocProvider JSON envelope that never
// leaves the process, so the passphrase never crosses a socket).
package enc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

const calver = "2026.182.0001"

// NewProvider builds the enc provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:enc + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). The verb is invoked with the structured
// spec.EncExecInput over OpExecute, not an authored plugin_input, so it declares no
// #*Input — only the trivial #EncInput ships so the host's plugin-schema gate has a
// non-empty, base-spliceable schema.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "enc"}},
		nil)
}

type provider struct {
	pb.UnimplementedProviderServer
}

// Invoke handles OpExecute: decode the host-prelifted spec.EncExecInput, run the
// selected gocryptfs mechanic, and return a spec.EncExecReply ({error}).
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpExecute {
		return nil, fmt.Errorf("enc: unsupported op %q (only %q)", req.GetOp(), sdk.OpExecute)
	}
	var in spec.EncExecInput
	if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
		return nil, fmt.Errorf("enc: decode input: %w", err)
	}
	out, err := json.Marshal(spec.EncExecReply{Error: run(in)})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// run dispatches on the method and returns the failure message ("" on success).
func run(in spec.EncExecInput) string {
	var err error
	switch in.Method {
	case spec.EncMethodMount:
		err = mountVolumes(in)
	case spec.EncMethodUnmount:
		err = unmountVolumes(in)
	case spec.EncMethodEnsure:
		err = ensureVolumes(in)
	case spec.EncMethodPasswd:
		err = passwdVolumes(in)
	default:
		return fmt.Sprintf("enc: unknown method %q", in.Method)
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// encExtpassArgs returns gocryptfs -extpass arguments for CLI commands. Uses a temp
// script file because gocryptfs's flag parser normalizes multi-flag values (turns -c
// into --c). The script checks GOCRYPTFS_PASSWORD env var first (set on the gocryptfs
// command by the mount/ensure paths), then falls back to systemd-ask-password with
// /dev/tty redirect so it works regardless of whether gocryptfs connects stdin to
// the child. Caller must defer the returned cleanup function.
func encExtpassArgs(imageID string) ([]string, func()) {
	script := "#!/bin/bash\n" +
		"if [ -n \"$GOCRYPTFS_PASSWORD\" ]; then\n" +
		"  printf '%s' \"$GOCRYPTFS_PASSWORD\"\n" +
		"else\n" +
		"  exec 0</dev/tty\n" +
		"  systemd-ask-password --id=" + imageID + " --timeout=0 --echo=masked 'Passphrase for " + imageID + ":'\n" +
		"fi\n"

	f, err := os.CreateTemp("", "charly-extpass-*.sh")
	if err != nil {
		// Fall back to inline systemd-ask-password (won't work headlessly)
		ep := "systemd-ask-password --id=" + imageID + " --timeout=0 --echo=masked Passphrase"
		return []string{"-extpass", ep}, func() {}
	}
	vmshared.RegisterTempCleanup(f.Name())
	if _, werr := f.WriteString(script); werr != nil {
		fmt.Fprintf(os.Stderr, "encExtpassArgs: write extpass script: %v\n", werr)
	}
	if cerr := f.Chmod(0700); cerr != nil {
		fmt.Fprintf(os.Stderr, "encExtpassArgs: chmod extpass script: %v\n", cerr)
	}
	_ = f.Close()
	return []string{"-extpass", f.Name()}, func() { _ = os.Remove(f.Name()); vmshared.UnregisterTempCleanup(f.Name()) }
}

// runGocryptfsScope runs `systemd-run --scope --user --unit=<scope> -- gocryptfs
// -allow_other <cipher> <plain>` with GOCRYPTFS_PASSWORD in the environment. On a
// mount failure it stops a stale scope from a previous run and retries once. Shared
// by mount + ensure (R3).
func runGocryptfsScope(scopeUnit string, extpassArgs []string, cipherDir, plainDir, passphrase string) error {
	gcArgs := append(slices.Clone(extpassArgs), "-allow_other", cipherDir, plainDir)
	scopeArgs := append([]string{"--scope", "--user", "--unit=" + scopeUnit, "--", "gocryptfs"}, gcArgs...)
	cmd := exec.Command("systemd-run", scopeArgs...)
	cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Stale scope from a previous run — stop it and retry.
		if stopErr := exec.Command("systemctl", "--user", "stop", scopeUnit+".scope").Run(); stopErr == nil {
			cmd = exec.Command("systemd-run", scopeArgs...)
			cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if retryErr := cmd.Run(); retryErr != nil {
				return retryErr
			}
		} else {
			return err
		}
	}
	return nil
}

// mountVolumes mounts every not-yet-mounted volume in the plan (EncMethodMount).
// The host-side shim already applied the all-mounted fast-path and the volume
// filter; a per-volume "not initialized" is a hard error, an already-mounted volume
// is skipped.
func mountVolumes(in spec.EncExecInput) error {
	extpassArgs, cleanup := encExtpassArgs(in.ImageID)
	defer cleanup()
	for _, m := range in.Volumes {
		if !m.Initialized {
			return fmt.Errorf("encrypted volume %q not initialized; run 'charly config %s' first", m.Name, in.BoxName)
		}
		if m.Mounted {
			fmt.Fprintf(os.Stderr, "%s: already mounted\n", m.Name)
			continue
		}
		if err := os.MkdirAll(m.PlainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}
		if err := runGocryptfsScope(m.ScopeUnit, extpassArgs, m.CipherDir, m.PlainDir, in.Passphrase); err != nil {
			return fmt.Errorf("mounting %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Mounted %s at %s\n", m.Name, m.PlainDir)
	}
	return nil
}

// ensureVolumes auto-initializes then mounts (EncMethodEnsure). The host-side shim
// invokes this only when at least one volume is not ready.
func ensureVolumes(in spec.EncExecInput) error {
	extpassArgs, cleanup := encExtpassArgs(in.ImageID)
	defer cleanup()
	for _, m := range in.Volumes {
		if !m.Initialized {
			fmt.Fprintf(os.Stderr, "Initializing encrypted volume %s for %s...\n", m.Name, in.BoxName)
			if err := os.MkdirAll(m.CipherDir, 0700); err != nil {
				return fmt.Errorf("creating cipher dir for %s: %w", m.Name, err)
			}
			if err := os.MkdirAll(m.PlainDir, 0700); err != nil {
				return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
			}
			args := append([]string{"-init"}, extpassArgs...)
			args = append(args, m.CipherDir)
			cmd := exec.Command("gocryptfs", args...)
			cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+in.Passphrase)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("gocryptfs -init for %s: %w", m.Name, err)
			}
		}
		if !m.Mounted {
			if err := os.MkdirAll(m.PlainDir, 0700); err != nil {
				return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
			}
			if err := runGocryptfsScope(m.ScopeUnit, extpassArgs, m.CipherDir, m.PlainDir, in.Passphrase); err != nil {
				return fmt.Errorf("mounting encrypted volume %s: %w", m.Name, err)
			}
			fmt.Fprintf(os.Stderr, "Mounted encrypted volume %s\n", m.Name)
		}
	}
	return nil
}

// unmountVolumes unmounts every mounted volume in the plan (EncMethodUnmount):
// fusermount3 -u then stop the gocryptfs scope unit (best-effort).
func unmountVolumes(in spec.EncExecInput) error {
	for _, m := range in.Volumes {
		if !m.Mounted {
			fmt.Fprintf(os.Stderr, "%s: not mounted\n", m.Name)
			continue
		}
		cmd := exec.Command("fusermount3", "-u", m.PlainDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("unmounting %s: %w", m.Name, err)
		}
		// Stop the gocryptfs scope unit (gocryptfs may linger after fusermount).
		_ = exec.Command("systemctl", "--user", "stop", m.ScopeUnit+".scope").Run() // best-effort
		fmt.Fprintf(os.Stderr, "Unmounted %s\n", m.Name)
	}
	return nil
}

// passwdVolumes changes the gocryptfs password for every initialized volume in the
// plan (EncMethodPasswd). The host-side shim already prompted for and validated the
// old/new passphrases and enforced the all-unmounted precondition.
func passwdVolumes(in spec.EncExecInput) error {
	for _, m := range in.Volumes {
		if !m.Initialized {
			fmt.Fprintf(os.Stderr, "%s: not initialized, skipping\n", m.Name)
			continue
		}
		// Create temp extpass script that supplies the old password.
		oldScript, err := os.CreateTemp("", "charly-oldpass-*.sh")
		if err != nil {
			return fmt.Errorf("creating temp script for %s: %w", m.Name, err)
		}
		vmshared.RegisterTempCleanup(oldScript.Name())
		if _, werr := oldScript.WriteString("#!/bin/bash\nprintf '%s' '" + strings.ReplaceAll(in.OldPass, "'", "'\\''") + "'\n"); werr != nil {
			return fmt.Errorf("writing temp script for %s: %w", m.Name, werr)
		}
		if cerr := oldScript.Chmod(0700); cerr != nil {
			return fmt.Errorf("chmod temp script for %s: %w", m.Name, cerr)
		}
		_ = oldScript.Close()

		// Pipe new password via stdin to gocryptfs -passwd.
		cmd := exec.Command("gocryptfs", "-passwd", "-extpass", oldScript.Name(), m.CipherDir)
		cmd.Stdin = strings.NewReader(in.NewPass)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		_ = os.Remove(oldScript.Name())
		vmshared.UnregisterTempCleanup(oldScript.Name())
		if runErr != nil {
			return fmt.Errorf("changing password for %s: %w", m.Name, runErr)
		}
		fmt.Fprintf(os.Stderr, "Password changed for %s\n", m.Name)
	}
	return nil
}
