package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// VmScpCmd copies a single LOCAL file into a running VM guest over SSH — the
// arbitrary-file analogue of `charly vm cp-box` (cp-box streams a whole
// container image; scp copies one file). It resolves the guest SSH endpoint the
// SAME way `charly vm ssh` / `charly vm cp-box` do (the managed charly-<name>
// ssh_config alias, via sshParamsForVm), so no host/port/key plumbing is
// needed, and a leading `~` in the destination resolves against the guest
// user's $HOME (faithful scp semantics).
type VmScpCmd struct {
	VM  string `arg:"" help:"kind:vm entity name (uses its managed charly-<name> ssh alias)"`
	Src string `arg:"" help:"local source file to copy into the guest (a leading ~ resolves against the host $HOME)"`
	Dst string `arg:"" help:"destination path in the guest (a leading ~ resolves against the guest user's $HOME)"`
}

func (c *VmScpCmd) Run() error {
	srcAbs, err := expandHostPath(c.Src)
	if err != nil {
		return fmt.Errorf("source %q: %w", c.Src, err)
	}
	return scpToVm(context.Background(), c.VM, srcAbs, c.Dst, "")
}

// scpToVm copies srcAbs (an already host-resolved local file) into the named VM
// guest at dst. The single host→guest single-file copy primitive (R3), shared
// by the `charly vm scp` subcommand and the harness credential sync
// (syncCredentialsToVM). It resolves the guest endpoint via sshParamsForVm — the
// same managed-alias resolution cp-box uses — then delegates to scpToVmExec.
func scpToVm(ctx context.Context, vmName, srcAbs, dst, modeStr string) error {
	return scpToVmExec(ctx, sshParamsForVm(vmName), srcAbs, dst, modeStr)
}

// scpToVmExec is the executor-injected seam of scpToVm (so the tilde-resolution
// + mode + USER-owned-delivery wiring is unit-testable without a live VM). It
// stats the source for its mode (modeStr overrides when non-empty), resolves a
// leading `~` in dst against the guest user's $HOME, and delivers the file via
// the executor's PutFile with ownerRoot=false — the file lands USER-owned, like
// the pod leg's `podman cp` into the guest user's home (credentials must be the
// AI user's, never root's).
func scpToVmExec(ctx context.Context, de DeployExecutor, srcAbs, dst, modeStr string) error {
	info, err := os.Stat(srcAbs)
	if err != nil {
		return fmt.Errorf("source %q unreadable: %w", srcAbs, err)
	}
	if info.IsDir() {
		return fmt.Errorf("source %q is a directory — charly vm scp copies a single file", srcAbs)
	}
	mode := parseTaskMode(modeStr, uint32(info.Mode().Perm()))

	if strings.HasPrefix(dst, "~") {
		home, herr := de.ResolveHome(ctx, "")
		if herr != nil {
			return fmt.Errorf("resolve guest $HOME for dst %q: %w", dst, herr)
		}
		dst = substTilde(dst, home)
	}

	if err := de.PutFile(ctx, srcAbs, dst, mode, false /*ownerRoot*/, EmitOpts{}); err != nil {
		return fmt.Errorf("scp %q -> guest:%q: %w", srcAbs, dst, err)
	}
	return nil
}
