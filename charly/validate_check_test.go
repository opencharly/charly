package main

import (
	"strings"
	"testing"
)

// opsCandy wraps a list of Ops as `check:` steps of a single candy plan so the
// per-Op validation rules (driven by validateOps walking plan steps) can be
// exercised in isolation.
func opsCandy(name string, ops ...Op) *Candy {
	steps := make([]Step, len(ops))
	for i := range ops {
		steps[i] = Step{Check: "check", Op: ops[i]}
	}
	return &Candy{Name: name, plan: steps}
}

// runValidateOps invokes validateOps against a synthetic fixture and returns the
// collected errors' text joined.
func runValidateOps(t *testing.T, cfg *Config, layers map[string]*Candy) string {
	t.Helper()
	errs := &ValidationError{}
	validateOps(cfg, layers, errs)
	return strings.Join(errs.Errors, "\n")
}

// A step bearing two verbs is rejected by Kind() at validation. A verbless step
// is a narrative-only (agent-graded) step and is intentionally NOT an error.
func TestValidateOps_MultiVerbRejected(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Copy: "/x", Mkdir: "/tmp/d"}),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "multiple verbs") {
		t.Errorf("expected 'multiple verbs' error: %s", got)
	}
}

// Port out-of-range and timeout parse failure.
// A build-context op may not reference runtime-only variables. command defaults
// to build+deploy+runtime, so with no explicit context it is build-legal and the
// runtime-only ${HOST_PORT} reference is flagged.
func TestValidateOps_RuntimeVarInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr",
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "runtime-only variable") || !strings.Contains(got, "HOST_PORT:6379") {
		t.Errorf("expected runtime-only variable error: %s", got)
	}
}

// Pinned to deploy context, the same op may use runtime variables — no error.
func TestValidateOps_RuntimeVarInDeployContext(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr",
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}, Context: []string{"deploy"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("unexpected errors: %s", got)
	}
}

// The former TestValidateOps_McpRejectedInBuildContext,
// TestValidateOps_McpCallRequiresTool, and TestValidateOps_McpReadRequiresURI were
// DELETED when mcp became an EXTERNAL-CHARLY-VERB (candy/plugin-mcp): mcp left
// VerbCatalog, so the host validateOps no longer enforces its runtime-only context
// (legality now rides the authored `context:` + the plugin's own box-mode skip) and its
// required-modifier checks (`mcp: call` needs tool, `mcp: read` needs uri) moved into the
// plugin at dispatch (methods.go's checkRequiredModifiers). The method-name enum is still
// enforced declaratively by CUE (#McpMethod).

// Valid mcp checks (default runtime context) produce no errors.
func TestValidateOps_McpClean(t *testing.T) {
	layers := map[string]*Candy{
		"jupyter": opsCandy("jupyter",
			Op{Mcp: "ping"},
			Op{Mcp: "list-tools"},
			Op{Mcp: "call", Tool: "list_notebooks", Input: "{}"},
			Op{Mcp: "read", URI: "file:///x"},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("clean mcp fixture produced errors: %s", got)
	}
}

// The former TestValidateOps_RecordRejectedInBuildContext,
// TestValidateOps_RecordStopRequiresArtifact, and TestValidateOps_RecordCmdRequiresText were
// DELETED when record became an EXTERNAL-CHARLY-VERB (candy/plugin-record): record left
// VerbCatalog, so the host validateOps no longer enforces its runtime-only context (legality
// now rides the authored `context:` + the plugin's own box-mode skip) and its
// required-modifier checks (`record: stop` needs artifact, `record: cmd` needs text) moved
// into the plugin at dispatch (methods.go's checkRequiredModifiers). The method-name enum is
// still enforced declaratively by CUE (#RecordMethod).

func TestValidateOps_RecordClean(t *testing.T) {
	layers := map[string]*Candy{
		"asciinema": opsCandy("asciinema",
			Op{Record: "list"},
			Op{Record: "start", RecordMode: "terminal"},
			Op{Record: "cmd", Text: "echo hi"},
			Op{Record: "stop", Artifact: "/tmp/demo.cast", ArtifactMinBytes: 100},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean record fixture produced errors: %s", got)
	}
}

// The former TestValidateOps_SpiceRejectedInBuildContext and
// TestValidateOps_SpiceTypeRequiresText were DELETED when spice became an
// EXTERNAL-CHARLY-VERB (candy/plugin-spice): spice left VerbCatalog, so the host
// validateOps no longer enforces its runtime-only context (legality now rides the
// authored `context:` + the plugin's own box-mode skip) and its required-modifier
// checks (`spice: type` needs text) moved into the plugin at dispatch
// (methods.go's checkRequiredModifiers). The method-name enum is still enforced
// declaratively by CUE (#SpiceMethod) — see cue_tighten_test.go's "candy check spice
// bogus method rejected". libvirt (still in-core) keeps its rejection tests below.

func TestValidateOps_SpiceClean(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm",
			Op{Spice: "status"},
			Op{Spice: "screenshot", Artifact: "/tmp/s.png"},
			Op{Spice: "type", Text: "hi"},
			Op{Spice: "key", KeyName: "Return"},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean spice fixture produced errors: %s", got)
	}
}

// TestValidateOps_LibvirtRejectedInBuildContext removed with libvirt's externalization: like
// kube/adb, libvirt left VerbCatalog (so core no longer holds its context-legality gate). A
// build-context libvirt op is now caught LOUDLY at build — emitPluginFragment finds no OpEmit
// fragment for a check verb — rather than at core authoring validation; the runtime-only contract
// is the external candy/plugin-vm's.

// TestValidateOps_LibvirtGuestExecRequiresCommand + ..._LibvirtSnapshotCreateRequiresTarget moved
// with the libvirt verb to candy/plugin-vm. libvirt is now an EXTERNAL-CHARLY-VERB, so its
// per-method argument validation (guest/exec needs a command, snapshot/create needs a target) is
// the plugin's LibvirtCmd Kong dispatch at runtime, NOT core authoring validation — exactly as for
// the other external verbs kube/adb (core no longer holds the libvirt method specs).

func TestValidateOps_LibvirtClean(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm",
			Op{Libvirt: "list"},
			Op{Libvirt: "info"},
			Op{Libvirt: "screenshot", Artifact: "/tmp/v.png"},
			Op{Libvirt: "guest/ping"},
			Op{Libvirt: "guest/exec", Command: "uname -r"},
			Op{Libvirt: "snapshot/create", Target: "pre-upgrade"},
			Op{Libvirt: "qmp", Text: "query-status"},
			Op{Libvirt: "send-key", KeyName: "ctrl alt F2"},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean libvirt fixture produced errors: %s", got)
	}
}

// Full valid fixture — candy plan + box plan — should produce no errors.
func TestValidateOps_Clean(t *testing.T) {
	layers := map[string]*Candy{
		"redis": opsCandy("redis",
			Op{Plugin: "file", PluginInput: map[string]any{"file": "/usr/bin/redis-server", "exists": true, "mode": "0755"}},
			Op{Plugin: "port", PluginInput: map[string]any{"port": 6379, "listening": true}},
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379} ping", "in_container": false}, Context: []string{"deploy"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{
		"redis-ml": {
			Enabled: new(true),
			Candy:   []string{"redis"},
			Plan: []Step{
				{Check: "version", Op: Op{ID: "version", Plugin: "command", PluginInput: map[string]any{"command": "redis-server --version"}}},
				{Check: "routed", Op: Op{ID: "routed", Plugin: "http", PluginInput: map[string]any{"http": "https://${DNS}/health", "status": 200}}},
			},
		},
	}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("clean fixture produced errors: %s", got)
	}
}

// Lowercase ${...} in a k8s identifier field is rejected — check variables are
// UPPERCASE, so a lowercase token never resolves and reaches the verb literally
// (the k3s-server "cluster: ${deploy_name}" class of bug). Uppercase is accepted,
// and a lowercase ${var} in a shell command body is NOT flagged (legit bash var).
func TestValidateOps_LowercaseCheckVarInClusterField(t *testing.T) {
	cfg := &Config{Box: map[string]BoxConfig{}}

	bad := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Kube: "addons", Cluster: "${deploy_name}", Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, bad); !strings.Contains(got, "UPPERCASE") || !strings.Contains(got, "${deploy_name}") {
		t.Errorf("expected lowercase-check-var rejection: %s", got)
	}

	ok := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Kube: "addons", Cluster: "${DEPLOY_NAME}", Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, ok); strings.Contains(got, "UPPERCASE") {
		t.Errorf("uppercase check var should pass: %s", got)
	}

	shell := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Plugin: "command", PluginInput: map[string]any{"command": `for v in ${name}; do echo "$v"; done`}, Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, shell); strings.Contains(got, "UPPERCASE") {
		t.Errorf("lowercase shell var in command must NOT be flagged: %s", got)
	}
}
