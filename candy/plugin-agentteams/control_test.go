package agentteams

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
)

// ---------------------------------------------------------------------------
// Token discovery (env → file → empty, the upstream agt runtime contract).
// ---------------------------------------------------------------------------

func TestDiscoverToken(t *testing.T) {
	t.Setenv("AGENTTEAMS_AUTH_TOKEN", "")
	t.Setenv("AGENTTEAMS_AUTH_TOKEN_FILE", "")

	// 1. env wins.
	t.Setenv("AGENTTEAMS_AUTH_TOKEN", "env-token")
	if got := discoverToken(); got != "env-token" {
		t.Fatalf("discoverToken() = %q, want env-token", got)
	}

	// 2. file when env is empty.
	t.Setenv("AGENTTEAMS_AUTH_TOKEN", "")
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTTEAMS_AUTH_TOKEN_FILE", tokenFile)
	if got := discoverToken(); got != "file-token" {
		t.Fatalf("discoverToken() = %q, want file-token", got)
	}

	// 3. empty when neither is set.
	t.Setenv("AGENTTEAMS_AUTH_TOKEN_FILE", "")
	if got := discoverToken(); got != "" {
		t.Fatalf("discoverToken() = %q, want empty", got)
	}

	// 4. missing file → empty (not an error).
	t.Setenv("AGENTTEAMS_AUTH_TOKEN_FILE", filepath.Join(dir, "does-not-exist"))
	if got := discoverToken(); got != "" {
		t.Fatalf("discoverToken() = %q, want empty for missing file", got)
	}
}

// ---------------------------------------------------------------------------
// YAML apply helpers.
// ---------------------------------------------------------------------------

func TestSplitYAMLDocs(t *testing.T) {
	input := `apiVersion: v1
kind: Worker
metadata:
  name: a
---
apiVersion: v1
kind: Team
metadata:
  name: b
`
	docs := splitYAMLDocs(input)
	if len(docs) != 2 {
		t.Fatalf("splitYAMLDocs() = %d docs, want 2", len(docs))
	}
	if !strings.Contains(docs[0], "name: a") || !strings.Contains(docs[1], "name: b") {
		t.Fatalf("splitYAMLDocs() split wrong: %q / %q", docs[0], docs[1])
	}
}

func TestSplitYAMLDocs_EmptyAndTrailing(t *testing.T) {
	docs := splitYAMLDocs("")
	if len(docs) != 0 {
		t.Fatalf("splitYAMLDocs(\"\") = %d docs, want 0", len(docs))
	}
	docs = splitYAMLDocs("---\n---\n")
	if len(docs) != 0 {
		t.Fatalf("splitYAMLDocs(separators only) = %d docs, want 0", len(docs))
	}
}

func TestBuildApplyBody(t *testing.T) {
	res := yamlResource{
		Metadata: yamlMetadata{Name: "w1"},
		Spec: map[string]interface{}{
			"model": "qwen3.6-plus",
		},
	}
	create := buildApplyBody(res, true)
	if create["name"] != "w1" {
		t.Fatalf("create body missing name: %#v", create)
	}
	if create["model"] != "qwen3.6-plus" {
		t.Fatalf("create body missing spec fields: %#v", create)
	}
	update := buildApplyBody(res, false)
	if _, ok := update["name"]; ok {
		t.Fatalf("update body must not carry name: %#v", update)
	}
	if update["model"] != "qwen3.6-plus" {
		t.Fatalf("update body missing spec fields: %#v", update)
	}
}

// ---------------------------------------------------------------------------
// Worker name validation.
// ---------------------------------------------------------------------------

func TestValidateWorkerName(t *testing.T) {
	// Mirrors the upstream agt pattern exactly: `^[a-z0-9][a-z0-9-]*$` — a
	// trailing hyphen is accepted, an uppercase/underscore/dot/leading-hyphen
	// is not.
	valid := []string{"w1", "worker-1", "a", "my-worker-2", "worker-"}
	for _, name := range valid {
		if err := validateWorkerName(name); err != nil {
			t.Fatalf("validateWorkerName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{"", "  ", "Worker", "worker_1", "worker.1", "-worker", "wö"}
	for _, name := range invalid {
		if err := validateWorkerName(name); err == nil {
			t.Fatalf("validateWorkerName(%q) = nil, want error", name)
		}
	}
}

// ---------------------------------------------------------------------------
// --expose parsing.
// ---------------------------------------------------------------------------

func TestParseExposePorts(t *testing.T) {
	ports, err := parseExposePorts("8080,3000")
	if err != nil {
		t.Fatalf("parseExposePorts(8080,3000) = %v", err)
	}
	if len(ports) != 2 {
		t.Fatalf("parseExposePorts() = %d ports, want 2", len(ports))
	}
	if ports[0]["port"] != 8080 || ports[1]["port"] != 3000 {
		t.Fatalf("parseExposePorts() = %#v, want [{port:8080} {port:3000}]", ports)
	}
	for _, bad := range []string{"", "abc", "0", "65536", "8080,8080", "8080,"} {
		if _, err := parseExposePorts(bad); err == nil {
			t.Fatalf("parseExposePorts(%q) = nil, want error", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// Package URI expansion.
// ---------------------------------------------------------------------------

func TestExpandPackageURI(t *testing.T) {
	t.Setenv("AGENTTEAMS_NACOS_REGISTRY_URI", "")

	// Full URIs pass through unchanged.
	for _, uri := range []string{"nacos://host:80/ns/pkg", "http://example.com/pkg.zip", "oss://bucket/key"} {
		got, err := expandPackageURI(uri)
		if err != nil || got != uri {
			t.Fatalf("expandPackageURI(%q) = %q, %v; want passthrough", uri, got, err)
		}
	}

	// Shorthand expands against the default registry.
	got, err := expandPackageURI("my-package")
	if err != nil {
		t.Fatalf("expandPackageURI(my-package) = %v", err)
	}
	if !strings.HasPrefix(got, "nacos://market.agentteams.io:80/public/") || !strings.HasSuffix(got, "/my-package") {
		t.Fatalf("expandPackageURI(my-package) = %q, want nacos://market.agentteams.io:80/public/my-package", got)
	}

	// Shorthand with a namespace path.
	got, err = expandPackageURI("org/team/pkg")
	if err != nil {
		t.Fatalf("expandPackageURI(org/team/pkg) = %v", err)
	}
	if !strings.HasSuffix(got, "/org/team/pkg") {
		t.Fatalf("expandPackageURI(org/team/pkg) = %q, want suffix /org/team/pkg", got)
	}

	// Custom registry base.
	t.Setenv("AGENTTEAMS_NACOS_REGISTRY_URI", "nacos://registry.example.com:8848/agentteams")
	got, err = expandPackageURI("pkg")
	if err != nil {
		t.Fatalf("expandPackageURI(pkg) with custom base = %v", err)
	}
	if got != "nacos://registry.example.com:8848/agentteams/pkg" {
		t.Fatalf("expandPackageURI(pkg) = %q, want nacos://registry.example.com:8848/agentteams/pkg", got)
	}

	// Invalid base.
	t.Setenv("AGENTTEAMS_NACOS_REGISTRY_URI", "http://not-nacos")
	if _, err := expandPackageURI("pkg"); err == nil {
		t.Fatal("expandPackageURI(pkg) with non-nacos base = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// ZIP manifest extraction.
// ---------------------------------------------------------------------------

func TestExtractWorkerFieldsFromZip(t *testing.T) {
	buildZip := func(manifest string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(manifest)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	// Top-level fields.
	model, runtime := extractWorkerFieldsFromZip(buildZip(`{"model":"qwen3.6-plus","runtime":"openclaw"}`))
	if model != "qwen3.6-plus" || runtime != "openclaw" {
		t.Fatalf("extractWorkerFieldsFromZip(top-level) = %q/%q, want qwen3.6-plus/openclaw", model, runtime)
	}

	// Worker block takes precedence.
	model, runtime = extractWorkerFieldsFromZip(buildZip(`{"model":"top","worker":{"model":"nested","runtime":"copaw"}}`))
	if model != "nested" || runtime != "copaw" {
		t.Fatalf("extractWorkerFieldsFromZip(worker block) = %q/%q, want nested/copaw", model, runtime)
	}

	// Missing manifest → empty.
	model, runtime = extractWorkerFieldsFromZip([]byte("not a zip"))
	if model != "" || runtime != "" {
		t.Fatalf("extractWorkerFieldsFromZip(bad zip) = %q/%q, want empty", model, runtime)
	}
}

// ---------------------------------------------------------------------------
// Misc helpers.
// ---------------------------------------------------------------------------

func TestDefaultWorkerModel(t *testing.T) {
	t.Setenv("AGENTTEAMS_DEFAULT_MODEL", "")
	if got := defaultWorkerModel(); got != "qwen3.6-plus" {
		t.Fatalf("defaultWorkerModel() = %q, want qwen3.6-plus", got)
	}
	t.Setenv("AGENTTEAMS_DEFAULT_MODEL", "my-model")
	if got := defaultWorkerModel(); got != "my-model" {
		t.Fatalf("defaultWorkerModel() = %q, want my-model", got)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("a, b ,,c")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitCSV() = %#v, want %#v", got, want)
		}
	}
	if got := splitCSV(""); len(got) != 0 {
		t.Fatalf("splitCSV(\"\") = %#v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Command model + in-proc dispatch (no network).
// ---------------------------------------------------------------------------

func TestCommandModel(t *testing.T) {
	model, err := commandModel()
	if err != nil {
		t.Fatalf("commandModel() = %v", err)
	}
	if model == nil {
		t.Fatal("commandModel() = nil model")
	}
	if model.Name != "agentteams" {
		t.Fatalf("commandModel().Name = %q, want agentteams", model.Name)
	}
}

func TestRunInProcCLI_Help(t *testing.T) {
	// --help must print and return nil WITHOUT running a leaf (the in-proc
	// dispatch hazard: kong's default Exit would os.Exit the host).
	var command AgentTeamsCmd
	err := sdk.RunInProcCLI("agentteams", &command, []string{"--help"},
		kong.Description("test"))
	if err != nil {
		t.Fatalf("RunInProcCLI(--help) = %v, want nil", err)
	}
}

func TestRunInProcCLI_Config(t *testing.T) {
	// `config` reads only env vars — no network — so it is a safe leaf to
	// dispatch in a unit test.
	t.Setenv("AGENTTEAMS_CONTROLLER_URL", "http://127.0.0.1:18090")
	t.Setenv("AGENTTEAMS_AUTH_TOKEN", "tok")
	var command AgentTeamsCmd
	err := sdk.RunInProcCLI("agentteams", &command, []string{"config"},
		kong.Description("test"))
	if err != nil {
		t.Fatalf("RunInProcCLI(config) = %v, want nil", err)
	}
}

func TestRunInProcCLI_UnknownCommand(t *testing.T) {
	var command AgentTeamsCmd
	err := sdk.RunInProcCLI("agentteams", &command, []string{"bogus"},
		kong.Description("test"))
	if err == nil {
		t.Fatal("RunInProcCLI(bogus) = nil, want parse error")
	}
}
