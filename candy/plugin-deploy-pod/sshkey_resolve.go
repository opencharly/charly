package deploypod

import (
	"fmt"

	"github.com/opencharly/spec/sshx"
)

// sshkey_resolve.go — the pod-config-ssh-key leg, relocated from
// charly/host_build_pod_config_seams.go's hostBuildPodConfigSSHKey (K-wave W3a B6).
// sshx.ContainerSSHKeyDir / sshx.ResolveSSHPubKey are already fully portable (spec/sshx, no
// core-only dependency) — this leg needed no seam at all, only a caller update. The
// "pod-config-ssh-key" HostBuild seam + its core handler are DELETED.
//
// Byte-for-byte parity with the deleted host body: an empty flag is a no-op (empty pubkey, nil
// error) — the deleted body's own first branch.

// resolveSSHPubKey resolves the SSH public key for a container's authorized_keys injection —
// pure host-FS reads (sshx.ContainerSSHKeyDir + sshx.ResolveSSHPubKey), no seam.
func resolveSSHPubKey(flag, containerName string) (string, error) {
	if flag == "" {
		return "", nil
	}
	sshDir, err := sshx.ContainerSSHKeyDir(containerName)
	if err != nil {
		return "", err
	}
	pubkey, err := sshx.ResolveSSHPubKey(flag, sshDir)
	if err != nil {
		return "", fmt.Errorf("resolving SSH key: %w", err)
	}
	return pubkey, nil
}
