package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ensures Kind() returns the correct verb for each single-verb Check and
// reports zero/multiple verbs as errors. The list-of-discriminators pattern
// mirrors Task.Kind() at charly/layers.go.
func TestCheck_Kind(t *testing.T) {
	tests := []struct {
		name    string
		check   Op
		wantKey string
		wantErr string // substring
	}{
		// `file` is NO LONGER a verb — it left #OpVerb in the file→plugin extraction and
		// is now `plugin: file` + #FileInput. A bare Op has no File field, so the file
		// CHECK is the generic plugin verb.
		{"file-as-plugin", Op{Plugin: "file", PluginInput: map[string]any{"file": "/usr/bin/redis"}}, "plugin", ""},
		// `package` is NO LONGER a verb — it left #OpVerb in the package→plugin
		// extraction and is now `plugin: package` + #PackageInput. A bare Op has no
		// Package field, so the package CHECK is the generic plugin verb.
		{"package-as-plugin", Op{Plugin: "package", PluginInput: map[string]any{"package": "redis"}}, "plugin", ""},
		// `service` is NO LONGER a verb — it left #OpVerb in the service→plugin
		// extraction and is now `plugin: service` + #ServiceInput. A bare Op has no
		// Service field, so the service CHECK is the generic plugin verb.
		{"service-as-plugin", Op{Plugin: "service", PluginInput: map[string]any{"service": "redis"}}, "plugin", ""},
		// `command` is NO LONGER a verb — it left #OpVerb in the command→plugin
		// extraction and is now a shared #Op modifier (wl/libvirt argv). A bare
		// Op.Command therefore yields NO verb; the command CHECK is `plugin: command`.
		{"command-modifier-not-verb", Op{Command: "redis-cli ping"}, "", "no verb"},
		{"command-as-plugin", Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli ping"}}, "plugin", ""},
		{"plugin", Op{Plugin: "matching"}, "plugin", ""},
		{"none", Op{}, "", "no verb"},
		{"two", Op{Copy: "/x", Mkdir: "/tmp/d"}, "", "multiple verbs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.check.Kind()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantKey {
					t.Errorf("Kind() = %q, want %q", got, tc.wantKey)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Kind() err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

// Round-trips a realistic tests: list through yaml.v3 to verify the struct
// tags produce the authoring shape documented in the plan.
func TestCheck_UnmarshalYAMLList(t *testing.T) {
	src := `
- plugin: file
  plugin_input:
    file: /usr/bin/redis-server
    exists: true
    mode: "0755"
- plugin: port
  plugin_input:
    port: 6379
    listening: true
- plugin: command
  plugin_input:
    command: redis-cli ping
  stdout: PONG
- plugin: http
  plugin_input:
    http: http://127.0.0.1:8888/api
    status: 200
    body:
      - contains: "ready"
- id: redis-responds
  context: [deploy]
  plugin: command
  plugin_input:
    command: redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ping
    in_container: false
  stdout: PONG
`
	var got []Op
	if err := decodeViaCUEForTest(t, src, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d checks, want 5", len(got))
	}

	// 0: file check — now the `file` plugin verb (authored as plugin: file +
	// plugin_input, validated against the file plugin's spliced #FileInput schema; the
	// file-exclusive fields + the shared mode ride plugin_input).
	if got[0].Plugin != "file" {
		t.Errorf("plugin = %q, want file", got[0].Plugin)
	}
	if got[0].PluginInput == nil || got[0].PluginInput["file"] != "/usr/bin/redis-server" {
		t.Errorf("plugin_input.file = %v, want /usr/bin/redis-server", got[0].PluginInput)
	}
	if got[0].PluginInput["exists"] != true {
		t.Errorf("plugin_input.exists = %v, want true", got[0].PluginInput["exists"])
	}
	if got[0].PluginInput["mode"] != "0755" {
		t.Errorf("plugin_input.mode = %v, want 0755", got[0].PluginInput["mode"])
	}

	// 1: port listening — now the `port` plugin verb (authored as plugin: port +
	// plugin_input, validated against the port plugin's spliced #PortInput schema).
	if got[1].Plugin != "port" {
		t.Errorf("plugin = %q, want port", got[1].Plugin)
	}
	if got[1].PluginInput == nil || got[1].PluginInput["listening"] != true {
		t.Errorf("plugin_input = %v, want a map with listening:true", got[1].PluginInput)
	}

	// 2: command with stdout matcher — now the `command` plugin verb (authored as
	// plugin: command + plugin_input.command); the stdout MATCHER stays at step level
	// (#Op), shared with the live verbs via matchAll.
	if got[2].Plugin != "command" {
		t.Errorf("plugin = %q, want command", got[2].Plugin)
	}
	if got[2].PluginInput == nil || got[2].PluginInput["command"] != "redis-cli ping" {
		t.Errorf("plugin_input.command = %v, want the cmd", got[2].PluginInput)
	}
	if len(got[2].Stdout) != 1 || got[2].Stdout[0].Op != "equals" || got[2].Stdout[0].Value != "PONG" {
		t.Errorf("stdout[0] = %+v, want {equals PONG}", got[2].Stdout)
	}

	// 3: http with body matcher — now the `http` plugin verb (authored as plugin: http +
	// plugin_input, validated against the http plugin's spliced #HttpInput schema).
	if got[3].Plugin != "http" {
		t.Errorf("plugin = %q, want http", got[3].Plugin)
	}
	if got[3].PluginInput == nil || got[3].PluginInput["http"] != "http://127.0.0.1:8888/api" {
		t.Errorf("plugin_input.http = %v, want the url", got[3].PluginInput)
	}
	if got[3].PluginInput["body"] == nil {
		t.Errorf("plugin_input.body missing: %v", got[3].PluginInput)
	}

	// 4: deploy-context command with runtime variable references
	if got[4].ID != "redis-responds" {
		t.Errorf("id = %q", got[4].ID)
	}
	if len(got[4].Context) != 1 || got[4].Context[0] != "deploy" {
		t.Errorf("context = %v, want [deploy]", got[4].Context)
	}
	if got[4].Plugin != "command" {
		t.Errorf("plugin = %q, want command", got[4].Plugin)
	}
	if got[4].PluginInput == nil || got[4].PluginInput["in_container"] != false {
		t.Errorf("plugin_input.in_container should be false, got %v", got[4].PluginInput)
	}
	cmd4, _ := got[4].PluginInput["command"].(string)
	if !strings.Contains(cmd4, "${HOST_PORT:6379}") {
		t.Errorf("plugin_input.command should preserve parameterized var ref: %q", cmd4)
	}
}

// Verifies Matcher decodes scalar, sequence, and single-key map forms.
func TestMatcher_UnmarshalYAML(t *testing.T) {
	type wrap struct {
		M []Matcher `yaml:"m"`
	}
	src := `
m:
  - PONG
  - equals: 42
  - contains:
      - "ok"
      - "ready"
  - matches: "^[a-z]+$"
  - [1, 2, 3]
`
	var w wrap
	if err := decodeViaCUEForTest(t, src, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(w.M) != 5 {
		t.Fatalf("got %d matchers, want 5", len(w.M))
	}
	cases := []struct {
		op    string
		value any
	}{
		// numeric matcher values decode as float64 via the CUE/JSON path
		// (Matcher.UnmarshalJSON), the live loader's decode mechanism.
		{"equals", "PONG"},
		{"equals", float64(42)},
		{"contains", []any{"ok", "ready"}},
		{"matches", "^[a-z]+$"},
		{"equals", []any{float64(1), float64(2), float64(3)}},
	}
	for i, want := range cases {
		if w.M[i].Op != want.op {
			t.Errorf("m[%d].op = %q, want %q", i, w.M[i].Op, want.op)
		}
		if !reflect.DeepEqual(w.M[i].Value, want.value) {
			t.Errorf("m[%d].value = %v (%T), want %v (%T)", i, w.M[i].Value, w.M[i].Value, want.value, want.value)
		}
	}
}

// Rejects a matcher map with more than one operator key.
func TestMatcher_RejectsMultiKey(t *testing.T) {
	src := `{equals: 1, contains: [2]}`
	var m Matcher
	if err := decodeViaCUEForTest(t, src, &m); err == nil {
		t.Fatal("expected error for multi-key matcher map, got nil")
	}
}

// Full-Check in-place expansion across all string-bearing fields.
func TestCheck_ExpandVars(t *testing.T) {
	// file is now a plugin verb; its path + owner ride plugin_input. opExpandVars walks the
	// PluginInput map (kit.ExpandAnyVars), so ${HOME} / ${MISSING} resolve there exactly as they
	// did when file/owner were base #Op string fields. Command stays an #Op modifier.
	c := Op{
		Plugin: "file",
		PluginInput: map[string]any{
			"file":  "${HOME}/.redis",
			"owner": "${MISSING}",
		},
		Command: "redis-cli -p ${HOST_PORT:6379}",
	}
	env := map[string]string{
		"HOME":           "/home/user",
		"HOST_PORT:6379": "16379",
	}
	missing := opExpandVars(&c, env)

	if got := c.PluginInput["file"]; got != "/home/user/.redis" {
		t.Errorf("plugin_input.file = %q", got)
	}
	if c.Command != "redis-cli -p 16379" {
		t.Errorf("Command = %q", c.Command)
	}
	if got := c.PluginInput["owner"]; got != "${MISSING}" {
		t.Errorf("plugin_input.owner should remain unresolved: %q", got)
	}
	wantMissing := []string{"MISSING"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}

// JSON-side scalar shorthand for Matcher / MatcherList: mirrors the YAML
// shorthand so hand-crafted OCI labels with `"stdout":"OK"` parse the
// same way as `stdout: OK` in the candy manifest.
func TestMatcher_UnmarshalJSON_Shorthand(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Matcher
	}{
		{"scalar string", `"OK"`, Matcher{Op: "equals", Value: "OK"}},
		{"scalar number", `42`, Matcher{Op: "equals", Value: float64(42)}},
		{"scalar bool", `true`, Matcher{Op: "equals", Value: true}},
		{"canonical map", `{"op":"contains","value":"ready"}`, Matcher{Op: "contains", Value: "ready"}},
		{"operator map", `{"matches":"^OK$"}`, Matcher{Op: "matches", Value: "^OK$"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Matcher
			if err := json.Unmarshal([]byte(tc.input), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if m.Op != tc.want.Op {
				t.Errorf("op = %q, want %q", m.Op, tc.want.Op)
			}
			if !reflect.DeepEqual(m.Value, tc.want.Value) {
				t.Errorf("value = %v (%T), want %v (%T)", m.Value, m.Value, tc.want.Value, tc.want.Value)
			}
		})
	}
}

// MatcherList JSON shorthand: array stays as-is, scalar/object becomes a
// one-element list. Closes the asymmetry with the YAML path.
func TestMatcherList_UnmarshalJSON_Shorthand(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantLen int
		wantOp  string
		wantVal any
	}{
		{"array", `[{"op":"equals","value":"A"},{"op":"contains","value":"B"}]`, 2, "equals", "A"},
		{"scalar", `"OK"`, 1, "equals", "OK"},
		{"object", `{"matches":"^go$"}`, 1, "matches", "^go$"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ml MatcherList
			if err := json.Unmarshal([]byte(tc.input), &ml); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(ml) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(ml), tc.wantLen)
			}
			if ml[0].Op != tc.wantOp {
				t.Errorf("op = %q, want %q", ml[0].Op, tc.wantOp)
			}
			if !reflect.DeepEqual(ml[0].Value, tc.wantVal) {
				t.Errorf("value = %v, want %v", ml[0].Value, tc.wantVal)
			}
		})
	}
}

// Verifies the extended ${NAME[:arg]} regex does not regress plain ${NAME}
// references (backward compatibility with deploykit.TaskVarRefPattern consumers).
func TestTestVarRefPattern_BackwardCompatible(t *testing.T) {
	got := TestVarRefs("${HOME}/x ${USER}")
	want := []string{"HOME", "USER"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The former TestContainsList_BareSequenceDefaultsToContains /
// TestContainsList_RealWorldHarnessProbe tested the base #Op `contains` load
// normalizer, which left #Op with the `file` verb when it was
// extracted into a plugin. The contains-default (a bare scalar → Op="contains") now
// lives in the file candy's decodeContainsList — covered by TestDecodeContainsList in
// candy/plugin-file.

// TestMatcherList_BareScalarDefaultsToEquals ensures a typed MatcherList field
// (stdout/body/…) keeps Op="equals" as the default for bare scalars, since
// `stdout: PONG` means "stdout EQUALS PONG", not "stdout CONTAINS PONG" — the field-name
// `contains:` semantic (Op="contains") is reproduced only in the file plugin's
// #FileContains, never on a typed MatcherList.
func TestMatcherList_BareScalarDefaultsToEquals(t *testing.T) {
	yamlSrc := `
command: echo PONG
stdout: PONG
`
	var c Op
	if err := decodeViaCUEForTest(t, yamlSrc, &c); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if len(c.Stdout) != 1 {
		t.Fatalf("len(Stdout) = %d, want 1", len(c.Stdout))
	}
	if c.Stdout[0].Op != "equals" {
		t.Errorf("MatcherList default = %q, want %q (a bare scalar on a typed MatcherList is equals, never contains)",
			c.Stdout[0].Op, "equals")
	}
}

// TestCheck_CaptureExtract_YAMLDecode covers the YAML surface for the
// 2026-05 capture_extract: regex modifier — paired with capture: it
// pulls a submatch from the value before storing in the
// ScenarioContext.Captures stash.

// DEPLOY_NAME is deploy-scope (resolved only against a live deployment), so a
// build-scope check referencing it must be rejected by the validator.
func TestIsRuntimeOnlyVar_DeployName(t *testing.T) {
	if !IsRuntimeOnlyVar("DEPLOY_NAME") {
		t.Error("DEPLOY_NAME must be runtime-only")
	}
}
