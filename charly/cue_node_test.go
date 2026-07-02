package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAgentPlugin_OutputFormat_RejectedAtLoad proves the `agent` plugin kind's served
// #AgentInput schema rejects an illegal output_format at LOAD. The agent kind was
// externalized into a dedicated plugin unit (candy/plugin-agent), so this validation
// moved from the core #NodeDoc gate to runPluginKind → validateAuthoredPluginInput
// against the plugin's #AgentInput (output_format: *"" | "stream-json"). A valid agent
// normalizes cleanly; `output_format: bogus` is a hard load error. It fails LOUDLY if
// the plugin schema ever stops covering it.
func TestAgentPlugin_OutputFormat_RejectedAtLoad(t *testing.T) {
	if err := normalizeAgentDoc(t, "myagent:\n  agent:\n    command: [\"x\"]\n    output_format: stream-json\n"); err != nil {
		t.Fatalf("valid agent (output_format: stream-json) was rejected at load: %v", err)
	}
	if err := normalizeAgentDoc(t, "myagent:\n  agent:\n    command: [\"x\"]\n    output_format: bogus\n"); err == nil {
		t.Fatal("illegal agent output_format 'bogus' was NOT rejected by the plugin #AgentInput schema at load")
	}
}

// normalizeAgentDoc runs a single-node doc through the parse + normalize path (the
// same one LoadUnified uses), so an `agent:` node is dispatched to runPluginKind and
// validated against the served #AgentInput. Returns the load error, if any.
func normalizeAgentDoc(t *testing.T, doc string) error {
	t.Helper()
	var d yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	_, nodes, err := parseNodeTree(&d)
	if err != nil {
		return err
	}
	uf := &UnifiedFile{}
	for _, gn := range nodes {
		if err := normalizeNodeInto(gn, uf); err != nil {
			return err
		}
	}
	return nil
}

// TestNodeFormSteps_RejectsStepTypo proves E1's plan-step typo gate: a node-form
// entity whose plan step carries an unknown Op field is rejected. Before E1 this
// passed silently — node-form steps are SIBLING nodes never typed against the closed
// #Step/#Op (validateNodeFormSteps, run at the validate entrypoint, closes that gap).
func TestNodeFormSteps_RejectsStepTypo(t *testing.T) {
	clean := "c:\n  candy:\n    version: \"2026.150.0000\"\n    description: x\n  s:\n    run: fetch the binary\n    download: \"http://example/x\"\n    extract: tar.gz\n"
	if err := validateNodeFormSteps("t", []byte(clean)); err != nil {
		t.Fatalf("clean candy plan step rejected: %v", err)
	}
	bad := "c:\n  candy:\n    version: \"2026.150.0000\"\n    description: x\n  s:\n    run: fetch the binary\n    download: \"http://example/x\"\n    extract: tar.gz\n    zz_bad_op_field: 1\n"
	if err := validateNodeFormSteps("t", []byte(bad)); err == nil {
		t.Fatal("a plan step with unknown Op field zz_bad_op_field was NOT rejected — the step-typo gate (E1) is broken")
	}
}

// nodeFormRejected reports whether the layered node-form strictness gates reject a
// document — the CUE document gate (closed kind VALUES + two-discriminator /
// reserved-key closedness) OR the Go parser (a typo'd discriminator, a wrong-kind /
// childless-kind child, a two-discriminator node). Both are hard load errors before
// any execution; together they are the "CUE-strict, no loosening" guarantee.
func nodeFormRejected(doc string) bool {
	if validateNodeDocCUE("t", []byte(doc)) != nil {
		return true
	}
	var d yaml.Node
	if yaml.Unmarshal([]byte(doc), &d) != nil {
		return true
	}
	_, nodes, err := parseNodeTree(&d)
	if err != nil {
		return true
	}
	// C2-group/C2-substrate/C2-candy: per-kind VALUE closedness moved from the #Node arms
	// (now an open struct) to the HOST-SIDE loader (runPluginKind → foldCandyKind /
	// foldSubstrateKind → validateKindValueCUE). Exercise the full node decode so a candy /
	// substrate value typo (an unknown inline field) is still caught by this "rejected?" helper.
	uf := &UnifiedFile{}
	for _, gn := range nodes {
		if normalizeNodeInto(gn, uf) != nil {
			return true
		}
	}
	return false
}

// nodeDocValid is a realistic unified node-form document (child-node): a distro, a
// candy whose package/env/composition and a plan step are each CHILD nodes, a box
// whose composition is a child, and a bundle group with bundle members, an inline
// cross-member check (${HOST:cache}) as a step child under a member, and a
// deeply-nested deploy-into with its own check child.
const nodeDocValid = `
version: "2026.180.0000"
fedora:
  distro:
    version: "43"
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
  redis-package:
    package: [redis]
  redis-env:
    env:
      REDIS_DATA: /var/lib/redis
  redis-binary:
    check: the binary exists
    file: /usr/bin/redis-server
coder:
  candy:
    base: fedora
  coder-candy:
    candy: [redis]
shop:
  bundle:
    disposable: true
  web:
    bundle:
      box: coder
    web-reaches-cache:
      check: web reaches the cache
      command: "redis-cli -h ${HOST:cache} ping"
  cache:
    bundle:
      box: coder
    migrate:
      bundle:
        box: coder
      migrate-ran:
        check: migration ran
        command: "test -f /done"
`

// TestValidateNodeDocCUE proves the Go CUE path (not just the cue CLI) enforces
// #Node strictness on a whole document: the valid node-form passes, and every
// strictness violation is a hard error.
func TestValidateNodeDocCUE(t *testing.T) {
	if err := validateNodeDocCUE("valid", []byte(nodeDocValid)); err != nil {
		t.Fatalf("valid node-form document rejected: %v", err)
	}

	bad := map[string]string{
		"typo-discriminator": `
fedora:
  distroo:
    version: "43"
`,
		"unknown-field-in-kind-value": `
redis:
  candy:
    version: "2026.150.0000"
    description: x
    statuz: working
`,
		// A sub-ENTITY (resource-kind) child under a non-deployable kind is
		// rejected (node_parse.go wrong-kind-child gate). EDGE-INHERIT cutover D
		// merged box: into candy:, and a `candy:` CHILD is the composition DATA key
		// (an image/layer can never be a deploy member), so the gate is exercised
		// with a genuine resource-kind sub-entity (pod/vm), not a candy: mapping.
		"resource-entity-under-childless-candy": `
redis:
  candy:
    version: "2026.150.0000"
    description: x
  inner:
    pod:
      image: x
`,
		"resource-entity-under-childless-distro": `
fedora:
  distro:
    bootstrap:
      install_cmd: "true"
  inner:
    vm:
      source: {kind: cloud_image, url: "http://x"}
`,
		"two-discriminators": `
db:
  vm:
    source: {kind: cloud_image, url: "http://x"}
  pod:
    image: y
`,
	}
	for name, doc := range bad {
		if !nodeFormRejected(doc) {
			t.Errorf("%s: expected a strictness rejection (CUE gate or parser), but the document was accepted", name)
		}
	}
}
