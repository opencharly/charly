package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// These tests pin the target-dispatch unification for local deploys: every
// surface that picks an executor for a `target: local` deployment now routes
// through ONE selection (rootExecutorForDeployNode) and ONE chain case (the
// `local` arm of AppendHopForFlatPath), instead of assuming a container. Each
// test FAILS against the pre-cutover code (no helper; no `local` chain case;
// resolveScoringChain fabricating an charly-<pod> container for a local target).

func TestRootExecutorForDeployNode(t *testing.T) {
	// nil node → host shell.
	if e, err := rootExecutorForDeployNode(nil); err != nil {
		t.Fatalf("nil node: %v", err)
	} else if _, ok := e.(ShellExecutor); !ok {
		t.Errorf("nil node → %T, want ShellExecutor", e)
	}

	// host: "" and host: "local" → host shell.
	for _, host := range []string{"", "local"} {
		e, err := rootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: host})
		if err != nil {
			t.Fatalf("host=%q: %v", host, err)
		}
		if _, ok := e.(ShellExecutor); !ok {
			t.Errorf("host=%q → %T, want ShellExecutor", host, e)
		}
	}

	// host: "user@box" → SSH with the inline user.
	e, err := rootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: "alice@box"})
	if err != nil {
		t.Fatalf("user@box: %v", err)
	}
	ssh, ok := e.(*SSHExecutor)
	if !ok {
		t.Fatalf("user@box → %T, want *SSHExecutor", e)
	}
	if ssh.User != "alice" || ssh.Host != "box" {
		t.Errorf("user@box → User=%q Host=%q, want alice/box", ssh.User, ssh.Host)
	}

	// host: "box" + user: "u" → SSH with the node.User (Ansible-style override).
	e, err = rootExecutorForDeployNode(&spec.BundleNode{Target: "local", Host: "box", User: "u"})
	if err != nil {
		t.Fatalf("box+user: %v", err)
	}
	ssh, ok = e.(*SSHExecutor)
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
	node, chain, err := ResolveDeployChain(stampTestDescents(roots), "workstation", ShellExecutor{})
	if err != nil {
		t.Fatalf("local node must resolve without error: %v", err)
	}
	if node == nil || node.Target != "local" {
		t.Fatalf("resolved node = %+v, want Target=local", node)
	}
	if _, ok := chain.(ShellExecutor); !ok {
		t.Errorf("local node added a hop: chain = %T, want ShellExecutor (no hop)", chain)
	}
}

// TestResolveScoringChain_Local: a flat score/bed target that resolves to a
// `target: local` node must run on the host venue, NOT a fabricated
// charly-<pod> container. Pre-cutover this returned a podman-exec NestedExecutor.
func TestResolveScoringChain_Local(t *testing.T) {
	roots := map[string]spec.BundleNode{
		"localbed": {Target: "local"},
		"podbed":   {Target: "pod"},
	}
	exec, err := resolveScoringChain(stampTestDescents(roots), "localbed")
	if err != nil {
		t.Fatalf("local bed: %v", err)
	}
	if _, ok := exec.(ShellExecutor); !ok {
		t.Errorf("local bed → %T, want ShellExecutor (host venue, not a container)", exec)
	}
	// A pod target still routes to a container chain (no regression).
	exec, err = resolveScoringChain(stampTestDescents(roots), "podbed")
	if err != nil {
		t.Fatalf("pod bed: %v", err)
	}
	if _, ok := exec.(*NestedExecutor); !ok {
		t.Errorf("pod bed → %T, want *NestedExecutor (container chain)", exec)
	}
}
