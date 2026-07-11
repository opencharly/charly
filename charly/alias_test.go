package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerateAliasScript(t *testing.T) {
	script := generateAliasScript("openclaw", "openclaw")

	if !strings.HasPrefix(script, "#!/bin/sh\n") {
		t.Error("script should start with shebang")
	}
	if !strings.Contains(script, aliasMarker) {
		t.Error("script should contain charly-alias marker")
	}
	if !strings.Contains(script, "# box: openclaw") {
		t.Error("script should contain box metadata")
	}
	if !strings.Contains(script, "# command: openclaw") {
		t.Error("script should contain command metadata")
	}
	if !strings.Contains(script, `exec charly shell openclaw -c "$c"`) {
		t.Errorf("script should contain exec charly shell line, got:\n%s", script)
	}
	if !strings.Contains(script, `_charly_q()`) {
		t.Error("script should contain _charly_q quoting helper")
	}
	if strings.Contains(script, "_exec") {
		t.Error("script should not contain _exec")
	}
}

func TestWriteAndListAliasScripts(t *testing.T) {
	dir := t.TempDir()

	if err := writeAliasScript(dir, "mycmd", "myimage", "mycommand"); err != nil {
		t.Fatalf("writeAliasScript() error = %v", err)
	}

	// Check file permissions
	path := filepath.Join(dir, "mycmd")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("file mode = %o, want 0755", info.Mode().Perm())
	}

	// List should find it
	aliases, err := listAliasScripts(dir)
	if err != nil {
		t.Fatalf("listAliasScripts() error = %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("listAliasScripts() returned %d aliases, want 1", len(aliases))
	}
	if aliases[0].Name != "mycmd" {
		t.Errorf("alias name = %q, want %q", aliases[0].Name, "mycmd")
	}
	if aliases[0].Box != "myimage" {
		t.Errorf("alias box = %q, want %q", aliases[0].Box, "myimage")
	}
	if aliases[0].Command != "mycommand" {
		t.Errorf("alias command = %q, want %q", aliases[0].Command, "mycommand")
	}
}

func TestRemoveAliasScript(t *testing.T) {
	dir := t.TempDir()

	if err := writeAliasScript(dir, "mycmd", "myimage", "mycommand"); err != nil {
		t.Fatalf("writeAliasScript() error = %v", err)
	}

	if err := removeAliasScript(dir, "mycmd"); err != nil {
		t.Fatalf("removeAliasScript() error = %v", err)
	}

	// Should be gone
	path := filepath.Join(dir, "mycmd")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

func TestRemoveAliasScriptNotCharlyAlias(t *testing.T) {
	dir := t.TempDir()

	// Write a non-charly file
	path := filepath.Join(dir, "notmine")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}

	err := removeAliasScript(dir, "notmine")
	if err == nil {
		t.Error("expected error when removing non-charly alias")
	}
	if !strings.Contains(err.Error(), "not an charly alias") {
		t.Errorf("unexpected error: %v", err)
	}

	// File should still exist
	if _, err := os.Stat(path); err != nil {
		t.Error("file should not be removed")
	}
}

func TestRemoveAliasScriptNotFound(t *testing.T) {
	dir := t.TempDir()

	err := removeAliasScript(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for missing alias")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCollectImageAliases(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	want := []CollectedAlias{{Name: "svc-cli", Command: "svc-cli-bin"}}
	if !reflect.DeepEqual(aliases, want) {
		t.Errorf("CollectBoxAlias() = %v, want %v", aliases, want)
	}
}

func TestCollectImageAliasesImageOverridesCandy(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{"svc"},
				Alias: []AliasConfig{{Name: "svc-cli", Command: "custom-cmd"}},
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Command != "custom-cmd" {
		t.Errorf("expected image override command, got %q", aliases[0].Command)
	}
}

func TestCollectImageAliasesDefaultCommand(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]BoxConfig{
			"myapp": {
				Candy: []string{"svc"},
				Alias: []AliasConfig{{Name: "mycli"}}, // no command
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name: "svc",
			plan: []Step{{Run: "build", Op: cmdOp("true")}},
		},
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Command != "mycli" {
		t.Errorf("expected command to default to name, got %q", aliases[0].Command)
	}
}

func TestCandyAliases(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if !ws.HasAliases() {
		t.Error("webservice should have aliases")
	}

	aliases := ws.Alias()
	if len(aliases) != 1 {
		t.Fatalf("Aliases() returned %d aliases, want 1", len(aliases))
	}
	if aliases[0].Name != "websvc" {
		t.Errorf("Aliases()[0].Name = %q, want %q", aliases[0].Name, "websvc")
	}
	if aliases[0].Command != "websvc-server" {
		t.Errorf("Aliases()[0].Command = %q, want %q", aliases[0].Command, "websvc-server")
	}
}

func TestAliasCandies(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	result := AliasCandy(layers)
	if len(result) != 1 {
		t.Errorf("AliasCandy() returned %d candies, want 1", len(result))
	}
	if len(result) > 0 && result[0].Name != "webservice" {
		t.Errorf("AliasCandy()[0].Name = %q, want %q", result[0].Name, "webservice")
	}
}

func TestListAliasScriptsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	aliases, err := listAliasScripts(dir)
	if err != nil {
		t.Fatalf("listAliasScripts() error = %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(aliases))
	}
}

func TestListAliasScriptsNonexistentDir(t *testing.T) {
	aliases, err := listAliasScripts("/nonexistent/path/12345")
	if err != nil {
		t.Fatalf("listAliasScripts() should not error for nonexistent dir, got: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(aliases))
	}
}

func TestAliasNameRegex(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"openclaw", true},
		{"my-tool", true},
		{"my_tool", true},
		{"my.tool", true},
		{"MyTool", true},
		{"tool123", true},
		{"1start", true},
		{"", false},
		{"-start", false},
		{".start", false},
		{"_start", false},
		{"has space", false},
		{"has/slash", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aliasNameRe.MatchString(tt.name)
			if got != tt.want {
				t.Errorf("aliasNameRe.MatchString(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
