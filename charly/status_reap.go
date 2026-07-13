package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// ReapOrphansCmd finds and cleans up orphaned ephemeral deployments —
// entries whose charly.yml ledger says "active" but whose underlying engine
// resource (libvirt domain, podman container, k8s namespace) is gone. Lifted
// out of the old `charly status --reap-orphans` flag so `charly status` stays
// single-purpose.
//
// Pure orphan detection — no race resolution. If a teardown is concurrently
// in progress, the second `charly bundle del --assume-yes` no-ops on the already-
// removed pieces.
type ReapOrphansCmd struct{}

func (c *ReapOrphansCmd) Run() error {
	dc, err := LoadBundleConfig()
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	if dc == nil {
		fmt.Println("no charly.yml; nothing to reap")
		return nil
	}
	var orphans []string
	for name, node := range dc.Bundle {
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if node.VmState.Ephemeral.Status != "active" {
			continue
		}
		if !ephemeralUnderlyingResourceAlive(name, node) {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) == 0 {
		fmt.Println("no orphaned ephemerals")
		return nil
	}
	for _, name := range orphans {
		fmt.Printf("reaping orphan %q ...\n", name)
		exe, _ := os.Executable()
		cmd := exec.Command(exe, deployDelArgv(name)...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: charly bundle del %q: %v\n", name, err)
		}
	}
	return nil
}

// ephemeralUnderlyingResourceAlive returns true when the named ephemeral's
// underlying resource is still alive. Best-effort across targets — false
// negatives are OK (we just skip reaping that entry); false positives are
// bad (we'd nuke a still-running resource), so the checks lean conservative.
func ephemeralUnderlyingResourceAlive(name string, node BundleNode) bool {
	switch node.Target {
	case "vm":
		domName := "charly-" + node.From
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			domName = "charly-" + node.VmState.Ephemeral.InstanceName
		}
		// Probe the domain via the out-of-process vm plugin (the go-libvirt impl moved there).
		raw, ok := invokeVmPlugin("domain-state", domName, "")
		if !ok {
			return true // can't probe → conservative: assume alive
		}
		var st struct {
			Exists bool `json:"exists"`
		}
		if json.Unmarshal(raw, &st) != nil {
			return true // decode failed → conservative
		}
		return st.Exists
	case "pod", "container":
		check := exec.Command("podman", "container", "exists", "charly-"+name)
		return check.Run() == nil
	case "k8s", "kubernetes":
		ns := name
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			ns = node.VmState.Ephemeral.InstanceName
		}
		check := exec.Command("kubectl", "get", "namespace", ns)
		check.Stderr = nil
		check.Stdout = nil
		return check.Run() == nil
	}
	return true // unknown target — conservative
}
