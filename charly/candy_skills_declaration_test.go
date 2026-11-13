package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestCandySkillsDeclaration_LoadGate proves the host-side kept-value gate ACCEPTS a candy's
// `skills:` declaration — the field that makes a candy's skill and agent TREES declarable beside
// it, each holding `<tree>/<name>/SKILL.md` plus `references/<stem>.md`, and `<tree>/<name>.md` for
// an agent.
//
// It fails on a spec pin that predates that def: there the gate answers
// `candy: skills: field not allowed` and the declaration cannot be authored at all, which blocks
// every consumer that writes one — including the relocation that moved a family's prose out of
// its manifest and into exactly these trees.
//
// The default is `.agents/skills` (the Agent-Skills standard location), so a repo whose skills live
// there declares nothing; the explicit form is asserted here because a DEFAULT that is never
// declarable is not a default, it is a hard-coded path.
func TestCandySkillsDeclaration_LoadGate(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	dir := t.TempDir()
	rootYAML := `discover:
    - path: candy
      recursive: true
postgresql:
    candy:
        description: Postgres 16 + contrib.
        skills:
            - .agents/skills
        plan:
            - check: /usr/bin/postgres exists
`
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("a candy declaring `skills:` must pass the kept-value load gate: %v", err)
	}
	il, ok := spec.DecodeInlineCandy(uf.Candy["postgresql"])
	if !ok {
		t.Fatalf("candy postgresql not loaded; candies=%v", mapKeys(uf.Candy))
	}
	// The declaration must survive the load, not merely be tolerated by it: the generator reads
	// this list to find a family's prose, so a gate that dropped it silently would be as broken as
	// one that refused it loudly.
	if len(il.Skills) != 1 || il.Skills[0] != ".agents/skills" {
		t.Errorf("the declared skill tree did not survive the load: Skills=%q, want [.agents/skills]", il.Skills)
	}
}
