package marketplace

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generate_test.go — the generate → drift proof on a realistic fixture: a marketplace entity, a
// per-candy skill, a concept-candy skill, hook entities, an existing .claude/settings.json with
// hand-owned keys, and a CLAUDE.md with the dispatcher markers. generate() writes every artifact;
// drift() is a no-op on the fresh output; a mutation makes drift fail (fail-closed).

const fixtureMarketplace = `marketplace:
    marketplace:
        name: charly-plugins
        version: 3.2.0
        description: Test marketplace.
        families:
            infrastructure:
                category: images
                description: Infrastructure services.
                keywords: [postgresql, infra]
                profiles: [user]
            core:
                category: commands
                description: Runtime verbs.
                profiles: [user]
`

const fixturePostgres = `postgresql:
    candy:
        version: 2026.218.1200
        description: Postgres 16 + contrib.
        plan:
            - check: /usr/bin/postgres exists
postgresql-skill:
    skill:
        name: postgresql
        family: infrastructure
        owner: postgresql
        description: Use when working with postgresql.
        content: |
            # Postgresql
            Start, configure, and probe postgresql.
        references:
            - name: configuration
              content: |
                # Configuration
                PGDATA lives under /var/lib/postgresql/data.
        triggers:
            - "postgres / postgresql / pg"
`

const fixtureCoreSkill = `charly-core:
    candy:
        version: 2026.218.1200
        description: Concept candy owning the core command skills.
        plan:
            - check: /bin/true
              command: "true"
charly-status-skill:
    skill:
        name: charly-status
        family: core
        owner: charly-core
        description: Show charly status.
        content: |
            # charly status
            Report pod health.
        triggers:
            - "charly status / status of a pod"
`

const fixtureHook = `charly-hooks:
    candy:
        version: 2026.218.1200
        description: Concept candy owning the harness gate hooks.
        plan:
            - check: /bin/true
              command: "true"
pre-commit-gate:
    hook:
        name: pre-commit-gate.sh
        trigger: PreToolUse
        matcher: Bash
        content: |
            #!/bin/bash
            # the pre-commit discipline gate
            exit 0
gitcmd:
    hook:
        name: gitcmd.py
        content: |
            # AUX file — not wired into settings.json
            pass
`

const fixtureSettings = `{
  "permissions": {
    "allow": ["Bash(charly check run:*)"]
  },
  "enabledPlugins": {
    "charly-core@charly-plugins": true,
    "claude-md-management@claude-plugins-official": true
  },
  "extraKnownMarketplaces": {
    "charly-plugins": {"source": {"source": "directory", "path": "./plugins"}}
  },
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "bash $CLAUDE_PROJECT_DIR/.claude/hooks/pre-commit-gate.sh"}]}
    ]
  }
}
`

const fixtureClaudeMD = `# Test

Intro.

<!-- BEGIN GENERATED SKILL DISPATCHER -->
stale row
<!-- END GENERATED SKILL DISPATCHER -->

Footer.
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("candy/charly-marketplace/charly.yml", fixtureMarketplace)
	write("candy/postgresql/charly.yml", fixturePostgres)
	write("candy/charly-core/charly.yml", fixtureCoreSkill)
	write("candy/charly-hooks/charly.yml", fixtureHook)
	write(".claude/settings.json", fixtureSettings)
	write("CLAUDE.md", fixtureClaudeMD)
	return dir
}

func TestGenerateThenDriftIsClean(t *testing.T) {
	dir := writeFixture(t)
	if err := generate(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertFile(t, dir, "plugins/infrastructure/skills/postgresql/SKILL.md",
		"name: postgresql", "Use when working with postgresql.", "# Postgresql")
	assertFile(t, dir, "plugins/infrastructure/skills/postgresql/references/configuration.md",
		"# Configuration")
	assertFile(t, dir, "plugins/core/skills/charly-status/SKILL.md", "name: charly-status")
	assertFile(t, dir, "plugins/infrastructure/.claude-plugin/plugin.json", "charly-infrastructure")
	assertFile(t, dir, "plugins/.claude-plugin/marketplace.json", "charly-plugins", "charly-core")
	assertFile(t, dir, "plugins/profiles.json", "charly-infrastructure")
	assertFile(t, dir, "plugins/.agents/plugins/marketplace.json", "charly-plugins", "./core", "INSTALLED_BY_DEFAULT")
	assertFile(t, dir, "plugins/kimi.plugin.json", `"name": "charly"`, "./core/skills", "./infrastructure/skills")
	assertFile(t, dir, "plugins/package.json", "opencharly-marketplace", "pi-package", "./*/skills")
	// per-plugin manifests carry NO version field (commit-SHA versioning — Claude Code docs).
	for _, rel := range []string{"plugins/core/.claude-plugin/plugin.json", "plugins/infrastructure/.claude-plugin/plugin.json"} {
		if strings.Contains(readFile(t, dir, rel), `"version"`) {
			t.Fatalf("%s must not carry a version field (commit-SHA versioning):\n%s", rel, readFile(t, dir, rel))
		}
	}
	assertFile(t, dir, ".claude/hooks/pre-commit-gate.sh", "pre-commit discipline")
	assertFile(t, dir, ".claude/hooks/gitcmd.py", "AUX file")
	assertFile(t, dir, ".claude/settings.json", "permissions", "claude-md-management@claude-plugins-official",
		"charly-core@charly-plugins", "charly-infrastructure@charly-plugins", "pre-commit-gate.sh", "opencharly/marketplace")
	assertFile(t, dir, "CLAUDE.md", "BEGIN GENERATED SKILL DISPATCHER", "postgres / postgresql / pg",
		"/charly-infrastructure:postgresql")
	// settings preserves the hand-owned keys (permissions + the official plugin) while
	// regenerating the charly-* enabledPlugins + the hooks wiring.
	settings := readFile(t, dir, ".claude/settings.json")
	if !strings.Contains(settings, `"permissions"`) || !strings.Contains(settings, `"claude-md-management@claude-plugins-official"`) {
		t.Fatalf("settings.json lost hand-owned keys:\n%s", settings)
	}

	// drift on the fresh output is a no-op (fail-closed gate).
	if err := drift(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("drift after generate must be clean: %v", err)
	}
}

// The success line is a CLAIM about what drift compared, and it is the claim the tool got wrong:
// it read "match the committed tree" while comparing bytes on disk, so it printed a git fact on a
// working tree `git status` called dirty. This fixture proves the semantics and not just the
// wording — it is a t.TempDir() that is never `git init`'d, so a check that consulted git could
// not report clean here at all, and the assertion fails the moment the message claims one.
func TestDriftCleanMessageNamesWhatItCompared(t *testing.T) {
	dir := writeFixture(t)
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fixture must NOT be a git repository — that is what makes this test discriminating: %v", err)
	}
	if err := generate(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("generate: %v", err)
	}
	var driftErr error
	out := captureStdout(t, func() { driftErr = drift(dir, filepath.Join(dir, "plugins")) })
	if driftErr != nil {
		t.Fatalf("drift after generate must be clean: %v", driftErr)
	}
	for _, want := range []string{"on disk match their sources", "regeneration is a no-op"} {
		if !strings.Contains(out, want) {
			t.Fatalf("clean message must state what was compared (%q), got: %q", want, out)
		}
	}
	if strings.Contains(out, "committed") {
		t.Fatalf("clean message must claim nothing about commit state — drift never reads git; got: %q", out)
	}
}

// captureStdout collects what fn writes to os.Stdout. fn must not call t.Fatal — record the error
// and assert after the return, or the restore below is skipped by runtime.Goexit.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	collected := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		collected <- b.String()
	}()
	fn()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-collected
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDriftFailsClosedOnMutation(t *testing.T) {
	dir := writeFixture(t)
	if err := generate(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Mutate a generated artifact — drift must FAIL.
	path := filepath.Join(dir, "plugins", "core", "skills", "charly-status", "SKILL.md")
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("MUTATED\n"), cur...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = drift(dir, filepath.Join(dir, "plugins"))
	if err == nil {
		t.Fatal("drift must fail after a mutation to a generated artifact")
	}
	if !strings.Contains(err.Error(), "core/skills/charly-status/SKILL.md") {
		t.Fatalf("drift error must name the drifted artifact, got: %v", err)
	}
}

func TestGeneratePrunesRemovedSkill(t *testing.T) {
	dir := writeFixture(t)
	if err := generate(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Remove the postgresql skill from the fixture — the SKILL.md must disappear.
	path := filepath.Join(dir, "candy", "postgresql", "charly.yml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(fixturePostgres, fixtureSkillBlock, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, filepath.Join(dir, "plugins")); err != nil {
		t.Fatalf("regenerate after skill removal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "charly-infrastructure", "skills", "postgresql", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("removed skill's SKILL.md must be pruned (err=%v)", err)
	}
}

func assertFile(t *testing.T, dir, rel string, wants ...string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	s := string(b)
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Fatalf("%s missing %q:\n%s", rel, w, s)
		}
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// fixtureSkillBlock is the skill node portion of fixturePostgres, for the prune test.
const fixtureSkillBlock = `
postgresql-skill:
    skill:
        name: postgresql
        family: infrastructure
        owner: postgresql
        description: Use when working with postgresql.
        content: |
            # Postgresql
            Start, configure, and probe postgresql.
        references:
            - name: configuration
              content: |
                # Configuration
                PGDATA lives under /var/lib/postgresql/data.
        triggers:
            - "postgres / postgresql / pg"
`

// TestGenerateSplitOut proves the cutover shape: the corpus lands under --out with the legacy
// "plugins/" prefix stripped, while the harness surface (.claude/, dispatcher) stays at root —
// the standalone-marketplace invocation (`charly marketplace generate --root <charly> --out
// <marketplace>`).
func TestGenerateSplitOut(t *testing.T) {
	dir := writeFixture(t)
	out := filepath.Join(dir, "marketplace")
	if err := generate(dir, out); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Corpus under out, prefix-free.
	assertFile(t, out, "core/skills/charly-status/SKILL.md", "name: charly-status")
	assertFile(t, out, "infrastructure/.claude-plugin/plugin.json", "charly-infrastructure")
	assertFile(t, out, ".claude-plugin/marketplace.json", "charly-plugins")
	assertFile(t, out, ".agents/plugins/marketplace.json", "./core")
	assertFile(t, out, "kimi.plugin.json", `"name": "charly"`)
	assertFile(t, out, "package.json", "opencharly-marketplace")
	assertFile(t, out, "profiles.json", "charly-infrastructure")
	assertFile(t, out, "setup", "charly marketplace")
	// Harness surface stays at root; the legacy corpus dir at <root>/plugins must NOT exist.
	assertFile(t, dir, ".claude/hooks/pre-commit-gate.sh", "pre-commit discipline")
	assertFile(t, dir, ".claude/settings.json", "charly-core@charly-plugins")
	assertFile(t, dir, "CLAUDE.md", "BEGIN GENERATED SKILL DISPATCHER")
	if _, err := os.Stat(filepath.Join(dir, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("legacy <root>/plugins must not exist when --out is given (err=%v)", err)
	}
	// drift against the split trees is a no-op.
	if err := drift(dir, out); err != nil {
		t.Fatalf("drift after split generate must be clean: %v", err)
	}
}
