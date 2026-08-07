package marketplace

import (
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
        settings:
            source_path: ./plugins
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
	if err := generate(dir); err != nil {
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
	assertFile(t, dir, ".claude/hooks/pre-commit-gate.sh", "pre-commit discipline")
	assertFile(t, dir, ".claude/hooks/gitcmd.py", "AUX file")
	assertFile(t, dir, ".claude/settings.json", "permissions", "claude-md-management@claude-plugins-official",
		"charly-core@charly-plugins", "charly-infrastructure@charly-plugins", "pre-commit-gate.sh")
	assertFile(t, dir, "CLAUDE.md", "BEGIN GENERATED SKILL DISPATCHER", "postgres / postgresql / pg",
		"/charly-infrastructure:postgresql")
	// settings preserves the hand-owned keys (permissions + the official plugin) while
	// regenerating the charly-* enabledPlugins + the hooks wiring.
	settings := readFile(t, dir, ".claude/settings.json")
	if !strings.Contains(settings, `"permissions"`) || !strings.Contains(settings, `"claude-md-management@claude-plugins-official"`) {
		t.Fatalf("settings.json lost hand-owned keys:\n%s", settings)
	}

	// drift on the fresh output is a no-op (fail-closed gate).
	if err := drift(dir); err != nil {
		t.Fatalf("drift after generate must be clean: %v", err)
	}
}

func TestDriftFailsClosedOnMutation(t *testing.T) {
	dir := writeFixture(t)
	if err := generate(dir); err != nil {
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
	err = drift(dir)
	if err == nil {
		t.Fatal("drift must fail after a mutation to a generated artifact")
	}
	if !strings.Contains(err.Error(), "core/skills/charly-status/SKILL.md") {
		t.Fatalf("drift error must name the drifted artifact, got: %v", err)
	}
}

func TestGeneratePrunesRemovedSkill(t *testing.T) {
	dir := writeFixture(t)
	if err := generate(dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Remove the postgresql skill from the fixture — the SKILL.md must disappear.
	path := filepath.Join(dir, "candy", "postgresql", "charly.yml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(fixturePostgres, fixtureSkillBlock, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir); err != nil {
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
