package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `# This is a comment
FOO=bar
BAZ="quoted value"
SINGLE='single quoted'
EMPTY=
NOVALUE

# Another comment
MULTI=hello world
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseEnvFile(envPath)
	if err != nil {
		t.Fatalf("ParseEnvFile() error: %v", err)
	}

	want := []string{
		"FOO=bar",
		"BAZ=quoted value",
		"SINGLE=single quoted",
		"EMPTY=",
		"NOVALUE",
		"MULTI=hello world",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseEnvFile() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestParseEnvBytes(t *testing.T) {
	content := []byte(`# This is a comment
FOO=bar
BAZ="quoted value"
SINGLE='single quoted'
EMPTY=
NOVALUE

# Another comment
MULTI=hello world
`)

	got, err := ParseEnvBytes(content)
	if err != nil {
		t.Fatalf("ParseEnvBytes() error: %v", err)
	}

	want := []string{
		"FOO=bar",
		"BAZ=quoted value",
		"SINGLE=single quoted",
		"EMPTY=",
		"NOVALUE",
		"MULTI=hello world",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseEnvBytes() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestParseEnvFileNotFound(t *testing.T) {
	_, err := ParseEnvFile("/nonexistent/.env")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadWorkspaceEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("KEY=value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadWorkspaceEnv(dir)
	if err != nil {
		t.Fatalf("LoadWorkspaceEnv() error: %v", err)
	}

	want := []string{"KEY=value"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadWorkspaceEnv() = %v, want %v", got, want)
	}
}

func TestLoadWorkspaceEnvNoFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadWorkspaceEnv(dir)
	if err != nil {
		t.Fatalf("LoadWorkspaceEnv() error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil when no .env file, got %v", got)
	}
}

func TestDeduplicateEnv(t *testing.T) {
	input := []string{"FOO=1", "BAR=2", "FOO=3"}
	got := deduplicateEnv(input)
	want := []string{"FOO=3", "BAR=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deduplicateEnv() = %v, want %v", got, want)
	}
}

func TestLoadProcessDotenv(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_DOTENV_VAR=from_dotenv\nTEST_DOTENV_EMPTY=\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = os.Unsetenv("TEST_DOTENV_VAR")
		_ = os.Unsetenv("TEST_DOTENV_EMPTY")
		resetDotenvLoaded()
	})

	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_VAR"); got != "from_dotenv" {
		t.Errorf("TEST_DOTENV_VAR = %q, want %q", got, "from_dotenv")
	}
	if !DotenvLoaded("TEST_DOTENV_VAR") {
		t.Error("TEST_DOTENV_VAR should be marked as dotenv-loaded")
	}
	if got := os.Getenv("TEST_DOTENV_EMPTY"); got != "" {
		t.Errorf("TEST_DOTENV_EMPTY = %q, want empty", got)
	}
	if !DotenvLoaded("TEST_DOTENV_EMPTY") {
		t.Error("TEST_DOTENV_EMPTY should be marked as dotenv-loaded")
	}
}

func TestLoadProcessDotenvRealEnvWins(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_EXISTING=from_dotenv\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_ = os.Setenv("TEST_EXISTING", "from_real_env")
	t.Cleanup(func() {
		_ = os.Unsetenv("TEST_EXISTING")
		resetDotenvLoaded()
	})

	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() error: %v", err)
	}

	if got := os.Getenv("TEST_EXISTING"); got != "from_real_env" {
		t.Errorf("TEST_EXISTING = %q, want %q (real env should win)", got, "from_real_env")
	}
	if DotenvLoaded("TEST_EXISTING") {
		t.Error("TEST_EXISTING should NOT be marked as dotenv-loaded (was already set)")
	}
}

func TestLoadProcessDotenvNoFile(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() should not error when .env missing, got: %v", err)
	}
}

func TestResolveEnvVars(t *testing.T) {
	dir := t.TempDir()

	// Create workspace .env
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("WS=workspace\nSHARED=ws\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create CLI env file
	cliEnvPath := filepath.Join(dir, "cli.env")
	if err := os.WriteFile(cliEnvPath, []byte("CLI_FILE=yes\nSHARED=cli-file\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveEnvVars(
		nil, // no global env
		[]string{"DEPLOY=yes", "SHARED=deploy"},
		"",
		dir,
		cliEnvPath,
		[]string{"CLI=flag", "SHARED=cli-flag"},
	)
	if err != nil {
		t.Fatalf("ResolveEnvVars() error: %v", err)
	}

	want := []string{
		"DEPLOY=yes",
		"SHARED=cli-flag", // CLI flag wins
		"WS=workspace",
		"CLI_FILE=yes",
		"CLI=flag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveEnvVars() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestNormalizeNoProxy(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"semicolons to commas", []string{"NO_PROXY=localhost;127.0.0.1;.internal"}, []string{"NO_PROXY=localhost,127.0.0.1,.internal"}},
		{"already commas", []string{"NO_PROXY=localhost,127.0.0.1"}, []string{"NO_PROXY=localhost,127.0.0.1"}},
		{"lowercase", []string{"no_proxy=localhost;127.0.0.1"}, []string{"no_proxy=localhost,127.0.0.1"}},
		{"other vars untouched", []string{"HTTP_PROXY=http://p:8080", "NO_PROXY=localhost;127.0.0.1"}, []string{"HTTP_PROXY=http://p:8080", "NO_PROXY=localhost,127.0.0.1"}},
		{"empty value", []string{"NO_PROXY="}, []string{"NO_PROXY="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeNoProxy(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeNoProxy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveEnvVarsNoProxyNormalized(t *testing.T) {
	got, err := ResolveEnvVars(
		nil, nil, "", "", "",
		[]string{"NO_PROXY=localhost;127.0.0.1"},
	)
	if err != nil {
		t.Fatalf("ResolveEnvVars() error: %v", err)
	}
	want := []string{"NO_PROXY=localhost,127.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveEnvVars() = %v, want %v", got, want)
	}
}

func TestResolveEnvVarsWithGlobalEnv(t *testing.T) {
	got, err := ResolveEnvVars(
		[]string{"GLOBAL=yes", "SHARED=global"}, // global env (lowest priority)
		[]string{"DEPLOY=yes", "SHARED=deploy"}, // per-image deploy env
		"",
		"",
		"",
		[]string{"CLI=flag"},
	)
	if err != nil {
		t.Fatalf("ResolveEnvVars() error: %v", err)
	}

	want := []string{
		"GLOBAL=yes",
		"SHARED=deploy", // per-image deploy overrides global
		"DEPLOY=yes",
		"CLI=flag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveEnvVars() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestEnrichNoProxy(t *testing.T) {
	tests := []struct {
		name           string
		envs           []string
		containerNames []string
		want           []string
	}{
		{
			"proxy with NO_PROXY appends container names",
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost,127.0.0.1,10.89.0.0/24"},
			[]string{"charly-immich-ml", "charly-jupyter"},
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost,127.0.0.1,10.89.0.0/24,charly-immich-ml,charly-jupyter"},
		},
		{
			"proxy without NO_PROXY creates it",
			[]string{"HTTPS_PROXY=http://1.2.3.4:8080"},
			[]string{"charly-immich-ml"},
			[]string{"HTTPS_PROXY=http://1.2.3.4:8080", "NO_PROXY=charly-immich-ml"},
		},
		{
			"no proxy leaves env unchanged",
			[]string{"FOO=bar", "NO_PROXY=localhost"},
			[]string{"charly-immich-ml"},
			[]string{"FOO=bar", "NO_PROXY=localhost"},
		},
		{
			"empty container names leaves env unchanged",
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost"},
			nil,
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost"},
		},
		{
			"duplicates not added",
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost,charly-immich-ml"},
			[]string{"charly-immich-ml", "charly-jupyter"},
			[]string{"HTTP_PROXY=http://1.2.3.4:8080", "NO_PROXY=localhost,charly-immich-ml,charly-jupyter"},
		},
		{
			"preserves lowercase no_proxy",
			[]string{"http_proxy=http://1.2.3.4:8080", "no_proxy=localhost"},
			[]string{"charly-immich-ml"},
			[]string{"http_proxy=http://1.2.3.4:8080", "no_proxy=localhost,charly-immich-ml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enrichNoProxy(tt.envs, tt.containerNames)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("enrichNoProxy() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}
