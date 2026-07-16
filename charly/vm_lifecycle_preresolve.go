package main

// vm_lifecycle_preresolve.go — the host-side DATA seam for the externalized vm substrate lifecycle.
// candy/plugin-deploy-vm does the whole venue lifecycle itself (ssh-config, auto-boot, guest waits,
// charly delivery, nested pods, start/stop) but cannot LoadUnified — it has no project. So the host
// resolves the kind:vm entity + its ssh coordinates + prior runtime state ONCE and ships them as
// spec.LifecyclePrepareInput on the OpPrepareVenue params. This is the SAME DATA-seam shape as the
// in-core k8s/android deployPreresolvers (registerDeployPreresolver) — NOT a hollow forward: the
// plugin consumes this data and does the real work. The grpcSubstrateLifecycle proxy consults the
// registry GENERICALLY (by word, never a "vm" branch), so pod (which registers none) is unaffected.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// lifecyclePrepareHook resolves the host-side DATA a substrate's OpPrepareVenue needs but cannot
// derive. Registered per substrate word at package-var init (like registerDeployPreresolver); the
// proxy consults it by word and threads the JSON under the "prepare" params key.
type lifecyclePrepareHook func(name, dir string, node *spec.BundleNode) (json.RawMessage, error)

var lifecyclePrepareHooks = map[string]lifecyclePrepareHook{}

func registerLifecyclePrepareHook(word string, fn lifecyclePrepareHook) {
	if word == "" || fn == nil {
		panic("registerLifecyclePrepareHook: empty word or nil fn")
	}
	if _, dup := lifecyclePrepareHooks[word]; dup {
		panic(fmt.Sprintf("registerLifecyclePrepareHook: duplicate hook for %q", word))
	}
	lifecyclePrepareHooks[word] = fn
}

func lifecyclePrepareHookFor(word string) (lifecyclePrepareHook, bool) {
	fn, ok := lifecyclePrepareHooks[word]
	return fn, ok
}

var _ = func() bool { registerLifecyclePrepareHook("vm", vmLifecyclePrepare); return true }()

// vmAttachResolver builds the F12 #PodLiveStdioPlan for `charly shell <vm-deploy>` / `charly cmd
// <vm-deploy>`: the vm's live venue executor is the guest *SSHExecutor (grpcSubstrateLifecycle.Attach
// resolves it via the vm VenueExecutor), whose RunInteractive wraps the script in `ssh -t <alias>
// [script]`. So the resolved script is just the in-guest command — the user's cmd joined, or empty for
// a bare interactive login shell (matching `charly vm ssh <alias>`). tty is immaterial for ssh (`-t`
// is always allocated on the interactive leg); the shell-vs-cmd distinction is a container concept.
func vmAttachResolver(_ context.Context, _, _ string, cmd []string, _ bool) (json.RawMessage, error) {
	return marshalJSON(&spec.PodLiveStdioPlan{Script: strings.Join(cmd, " ")})
}

var _ = func() bool { registerLifecycleLivePlanHooks("vm", vmAttachResolver, nil); return true }()

// vmLifecyclePrepare resolves the kind:vm entity + ssh coordinates + prior VmDeployState for the vm
// plugin's OpPrepareVenue. It reproduces the RESOLUTION half of the former vmSubstrateLifecycle.
// PrepareVenue (entity, spec.Vm, ssh user/port, state dir, prior state) + runs the one host-side
// Add-time side effect it cannot delegate (registerEphemeralIfMarked). The ACTIONS (ssh-config
// stanza, auto-boot, guest waits, charly delivery) are the plugin's job now.
func vmLifecyclePrepare(name, dir string, node *spec.BundleNode) (json.RawMessage, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("vm deploy %q: resolve deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("vm deploy %q: no deploy entry", name)
		}
		node = &n
	}

	vmName, err := vmEntityForAdd(node, name)
	if err != nil {
		return nil, err
	}

	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ok || uf.VM == nil {
		return nil, fmt.Errorf("vm deploy %q: no charly.yml or no kind:vm entities declared", name)
	}
	body, ok := uf.VM[vmName]
	if !ok {
		return nil, fmt.Errorf("vm deploy %q: no kind:vm entity named %q in charly.yml", name, vmName)
	}
	vmSpec, err := resolveVmViaPlugin(body)
	if err != nil {
		return nil, err
	}
	if vmSpec == nil {
		return nil, fmt.Errorf("vm deploy %q: kind:vm entity %q resolved to an empty value", name, vmName)
	}

	// Ephemeral lifecycle hook (the one Add-time host side effect — panic-safe TTL ordering). Consumes
	// the MERGED node (never a charly.yml re-read).
	registerEphemeralIfMarked(node, name)

	// Prior runtime state (instance-id, ssh_port, disk path) for the plugin's port idempotency.
	var state *spec.VmDeployState
	if dc := deploykit.LoadDeployConfigForRead("charly bundle add vm"); dc != nil {
		if entry, exists := dc.Bundle[name]; exists && entry.VmState != nil {
			state = entry.VmState
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home dir: %w", err)
	}
	// The libvirt domain, per-domain state dir, managed ssh alias, and ssh-port ledger key off the
	// per-deploy DOMAIN IDENTITY (the deploy name), NOT the shared kind:vm entity — so sibling beds
	// referencing one entity get distinct, collision-free domains + disks + ports. The plugin derives
	// the SAME identity from the SAME deploy name (vmshared.VmDomainIdentity), so the two agree. Entity
	// stays the disk/spec source (`vm build` builds it; each deploy overlays it).
	domainID := vmDomainIdentity(name)
	stateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+domainID)

	sshPort, err := resolveVmSshPort(vmSpec, domainID)
	if err != nil {
		return nil, err
	}

	in := spec.LifecyclePrepareInput{
		Entity:         vmName,
		VM:             vmSpec,
		SSHUser:        resolveVmSshUser(vmSpec),
		SSHPort:        sshPort,
		Alias:          VmSshAlias(domainID),
		SSHKeyPath:     filepath.Join(stateDir, "id_ed25519"),
		KnownHostsPath: filepath.Join(stateDir, "known_hosts"),
		StateDir:       stateDir,
		PriorState:     state,
	}
	return json.Marshal(in)
}

// lifecyclePostTeardownHook runs host-side substrate cleanup AFTER the plugin's OpPostTeardown that
// the plugin cannot do (it uses core-only machinery). Registered per word; the proxy consults it.
type lifecyclePostTeardownHook func(name string, node *spec.BundleNode) error

var lifecyclePostTeardownHooks = map[string]lifecyclePostTeardownHook{}

func registerLifecyclePostTeardownHook(word string, fn lifecyclePostTeardownHook) {
	if word == "" || fn == nil {
		panic("registerLifecyclePostTeardownHook: empty word or nil fn")
	}
	if _, dup := lifecyclePostTeardownHooks[word]; dup {
		panic(fmt.Sprintf("registerLifecyclePostTeardownHook: duplicate hook for %q", word))
	}
	lifecyclePostTeardownHooks[word] = fn
}

func lifecyclePostTeardownHookFor(word string) (lifecyclePostTeardownHook, bool) {
	fn, ok := lifecyclePostTeardownHooks[word]
	return fn, ok
}

var _ = func() bool { registerLifecyclePostTeardownHook("vm", vmLifecyclePostTeardown); return true }()

// vmLifecyclePostTeardown runs the vm ephemeral-lifecycle teardown host-side (systemd transient
// timers + libvirt snapshot refcounts — un-importable by the plugin). The ssh-config stanza + the
// charly.yml entry removal are the plugin's job (kit.RemoveVmSshStanza + PostTeardownReply.RemoveEntries).
func vmLifecyclePostTeardown(name string, node *spec.BundleNode) error {
	if dcNode, ok := deploykit.LoadDeployConfigForRead("vm ephemeral-teardown").LookupKey(name); ok && dcNode.IsEphemeral() {
		return TeardownEphemeralLifecycle(&dcNode, name)
	}
	return nil
}

// vmEntityForAdd resolves the kind:vm entity name for an add. Prefers the merged node's `vm:`
// cross-ref (the canonical mapping for a schema-v4 deploy where the key != entity, e.g. check-k3s-vm
// → vm: k3s-vm); falls back to stripping a legacy "vm:<name>" deploy-key prefix, then to the leaf of
// a nested dotted path (stack.myvm → myvm). Relocated here from the deleted vm_deploy_lifecycle.go
// (the last surviving consumer is this preresolver + the del path).
func vmEntityForAdd(node *spec.BundleNode, name string) (string, error) {
	if node != nil && node.From != "" {
		return node.From, nil
	}
	if strings.HasPrefix(name, "vm:") {
		return vmNameFromDeployName(name)
	}
	if strings.Contains(name, ".") {
		return pathLeaf(name), nil
	}
	return "", fmt.Errorf("vm deploy %q: no `vm:` cross-ref and key is not a legacy vm:<name> form", name)
}
