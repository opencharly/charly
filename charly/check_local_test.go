package main

import (
	"testing"

	"github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// These tests pin the target-dispatch unification for local deploys: every
// surface that picks an executor for a `target: local` deployment now routes
// through ONE selection (rootExecutorForDeployNode) and ONE chain case (the
// `local` arm of AppendHopForFlatPath), instead of assuming a container. Each
// test FAILS against the pre-cutover code (no helper; no `local` chain case).
// The THIRD historical test in this file, TestResolveScoringChain_Local (pinning the AI
// harness's scoring-chain resolver not fabricating an charly-<pod> container for a local
// target), moved to candy/plugin-check/score_live_test.go's TestPluginResolveScoringChain_Local
// (K1-unblock wave arm 3 — the scoring-chain resolver itself moved plugin-side).

// stampTestDescents stamps the descent descriptor from the substrate's declared #DeployTraits
// (via charly's own deployTraitsFor registry lookup + spec.StampDescent directly — #55 K4 cone3:
// moved here, its only remaining charly caller, when the duplicate charly/deploy_chain_test.go was
// deleted in favor of the already-relocated candy/plugin-bundle/deploy_chain_test.go twin), so a
// BundleNode literal built directly in a test (bypassing the loader) runs against a
// realistically-stamped node instead of tripping the nil-descent guard.
func stampTestDescents(roots map[string]spec.BundleNode) map[string]spec.BundleNode {
	out := make(map[string]spec.BundleNode, len(roots))
	for k, v := range roots {
		n := v
		spec.StampDescent(&n, deployTraitsFor)
		out[k] = n
	}
	return out
}

func TestRootExecutorForDeployNode(t *testing.T) {
	// nil node → host shell.
	if e, err := exec.RootExecutorForDeployNode(nil); err != nil {
		t.Fatalf("nil node: %v", err)
	} else if _, ok := e.(exec.ShellExecutor); !ok {
		t.Errorf("nil node → %T, want ShellExecutor", e)
	}

	// host: "" and host: "local" → host shell.
	for _, host := range []string{"", "local"} {
		e, err := exec.RootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: host})
		if err != nil {
			t.Fatalf("host=%q: %v", host, err)
		}
		if _, ok := e.(exec.ShellExecutor); !ok {
			t.Errorf("host=%q → %T, want ShellExecutor", host, e)
		}
	}

	// host: "user@box" → SSH with the inline user.
	e, err := exec.RootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: "alice@box"})
	if err != nil {
		t.Fatalf("user@box: %v", err)
	}
	ssh, ok := e.(*exec.SSHExecutor)
	if !ok {
		t.Fatalf("user@box → %T, want *SSHExecutor", e)
	}
	if ssh.User != "alice" || ssh.Host != "box" {
		t.Errorf("user@box → User=%q Host=%q, want alice/box", ssh.User, ssh.Host)
	}

	// host: "box" + user: "u" → SSH with the node.User (Ansible-style override).
	e, err = exec.RootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: "box", User: "u"})
	if err != nil {
		t.Fatalf("box+user: %v", err)
	}
	ssh, ok = e.(*exec.SSHExecutor)
	if !ok {
		t.Fatalf("box+user → %T, want *SSHExecutor", e)
	}
	if ssh.User != "u" || ssh.Host != "box" {
		t.Errorf("box+user → User=%q Host=%q, want u/box", ssh.User, ssh.Host)
	}
}

// TestResolveDeployChain_LocalNoHop: a `target: local` root node must resolve
// (no error) and add NO hop — the chain stays at the passed-in root executor.
// Pre-cutover, AppendHopForFlatPath had no `local` case → "unknown target".
func TestResolveDeployChain_LocalNoHop(t *testing.T) {
	roots := map[string]spec.BundleNode{
		"workstation": {Target: "local"},
	}
	node, chain, err := exec.ResolveDeployChain(stampTestDescents(roots), "workstation", exec.ShellExecutor{})
	if err != nil {
		t.Fatalf("local node must resolve without error: %v", err)
	}
	if node == nil || node.Target != "local" {
		t.Fatalf("resolved node = %+v, want Target=local", node)
	}
	if _, ok := chain.(exec.ShellExecutor); !ok {
		t.Errorf("local node added a hop: chain = %T, want ShellExecutor (no hop)", chain)
	}
}
