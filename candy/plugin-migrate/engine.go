package migrate

// engine.go — the CUE-anchored declarative migration engine (`charly
// migrate`). It replaced the retired 47-step hand-written chain (candy/plugin-
// migrate) at the migration-baseline reset. There are two moving parts:
//
//   1. A CUE-owned schema version (sdk/schema/version.cue → spec.SchemaVersion /
//      spec.SchemaFloor, parsed by kit.LatestSchemaVersion() / kit.SchemaFloor()).
//   2. A declarative migration TABLE (charly/migrations.cue), validated at process
//      start against #Migration and interpreted by ONE generic op-walker. A future
//      migration is DATA (rename_key / delete_key / remap_scalar / move_key) — zero
//      new Go for the common case; a structural reshape registers one goHooks entry.
//
// runMigrations is floor-gated: a config AT head is a no-op; a config BELOW the
// floor is unmigratable (the chain that once handled older formats is gone); a
// config in [floor, head) runs the table's newer steps then re-stamps to head. At
// the reset the table is empty and floor == head, so `charly migrate` only ever
// says "nothing to migrate" or refuses a below-floor config — the honest state
// after dropping the migration history; the engine is scaffolding for the future.

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
	sdkschema "github.com/opencharly/sdk/schema"
	"github.com/opencharly/sdk/schemaconcat"
)

// migCtx is the plugin-local CUE context (this plugin owns the migration engine, so it
// no longer shares charly's core cueSchemaCtx). migrationSchema is #Migration compiled
// standalone from the SDK's schema — migration.cue (#Migration) + version.cue
// (#CanonCalVer, which #Migration.version pins to) concatenated ALONE, so the plugin
// validates the table WITHOUT pulling charly's full ingress schema and WITHOUT
// duplicating #CanonCalVer (which must stay the SDK's single source of truth).
var (
	migCtx          = cuecontext.New()
	migrationSchema = compileMigrationDefs()
)

func compileMigrationDefs() cue.Value {
	body, _, err := schemaconcat.ConcatSchema(sdkschema.FS, ".", func(name string) bool {
		return name != "migration.cue" && name != "version.cue"
	})
	if err != nil {
		panic(fmt.Sprintf("compileMigrationDefs: concat #Migration schema: %v", err))
	}
	v := migCtx.CompileString(body)
	if v.Err() != nil {
		panic(fmt.Sprintf("compileMigrationDefs: #Migration schema does not compile: %v", cueerrors.Details(v.Err(), nil)))
	}
	return v
}

// migrationsCUE is the declarative migration table (charly/migrations.cue). It is
// engine DATA, not ingress schema — it lives outside sdk/schema/ so it never
// enters the spec codegen / vocab concatenation.
//
//go:embed migrations.cue
var migrationsCUE []byte

// migration is one decoded table step. Exactly one of Ops / Apply is set.
type migration struct {
	Version     kit.CalVer
	Name        string
	TouchesHost bool
	Ops         []migrationOp
	Apply       string // names a goHooks entry (the structural-reshape escape hatch)
}

// migrationOp is one declarative op. The relevant fields depend on Op (validated
// against #MigrationOp's closed arms in migration.cue before decode).
type migrationOp struct {
	Op         string `json:"op"`
	From       string `json:"from"`
	To         string `json:"to"`
	Key        string `json:"key"`
	Scope      string `json:"scope"`
	UnderKind  string `json:"under_kind"`
	FromParent string `json:"from_parent"`
	ToParent   string `json:"to_parent"`
}

// goHooks holds the structural-reshape escape hatches a declarative step names via
// `apply:`. Registered HERE in the literal (never via init() — the migrationTable
// var below validates hook names during var initialization, which precedes every
// init() but follows this literal in dependency order).
var goHooks = map[string]func(*yaml.Node) bool{
	"compactNodeForm": compactNodeForm, // the schema-compaction reshaper (reshape_compact.go)
}

// migrationTable is the validated, ascending-ordered step list, loaded once at
// process start. A malformed table panics here (fail-fast, like registerCueKind).
var migrationTable = loadMigrationTable()

// loadMigrationTable compiles charly/migrations.cue, validates each entry against
// #Migration (from the shared compiled schema), enforces exactly-one ops/apply +
// strictly-ascending canonical versions + a registered hook name, and decodes.
func loadMigrationTable() []migration {
	v := migCtx.CompileString(string(migrationsCUE))
	if v.Err() != nil {
		panic(fmt.Sprintf("migrations.cue failed to compile: %v", cueerrors.Details(v.Err(), nil)))
	}
	list := v.LookupPath(cue.ParsePath("migrations"))
	if !list.Exists() {
		panic("migrations.cue: missing top-level `migrations:` list")
	}
	migDef := migrationSchema.LookupPath(cue.ParsePath("#Migration"))
	if migDef.Err() != nil {
		panic(fmt.Sprintf("#Migration schema not found: %v", migDef.Err()))
	}
	iter, err := list.List()
	if err != nil {
		panic(fmt.Sprintf("migrations.cue: `migrations:` is not a list: %v", err))
	}
	var out []migration
	var prev kit.CalVer
	for i := 0; iter.Next(); i++ {
		elem := iter.Value()
		if verr := elem.Unify(migDef).Validate(cue.Concrete(true)); verr != nil {
			panic(fmt.Sprintf("migrations.cue: step %d invalid: %v", i, cueerrors.Details(verr, nil)))
		}
		var raw struct {
			Version     string        `json:"version"`
			Name        string        `json:"name"`
			TouchesHost bool          `json:"touches_host"`
			Ops         []migrationOp `json:"ops"`
			Apply       string        `json:"apply"`
		}
		if derr := elem.Decode(&raw); derr != nil {
			panic(fmt.Sprintf("migrations.cue: step %d decode: %v", i, derr))
		}
		if (len(raw.Ops) > 0) == (raw.Apply != "") {
			panic(fmt.Sprintf("migrations.cue: step %q must set EXACTLY one of `ops:` or `apply:`", raw.Name))
		}
		if raw.Apply != "" {
			if _, ok := goHooks[raw.Apply]; !ok {
				panic(fmt.Sprintf("migrations.cue: step %q names unknown Go hook %q (register it in goHooks)", raw.Name, raw.Apply))
			}
		}
		ver, ok := kit.ParseCalVer(raw.Version)
		if !ok {
			panic(fmt.Sprintf("migrations.cue: step %q has non-canonical version %q", raw.Name, raw.Version))
		}
		if i > 0 && !prev.Less(ver) {
			panic(fmt.Sprintf("migrations.cue: step %q version %s is not strictly after the previous step %s", raw.Name, ver, prev))
		}
		if ver.Less(kit.SchemaFloor()) || kit.LatestSchemaVersion().Less(ver) {
			panic(fmt.Sprintf("migrations.cue: step %q version %s is outside the migratable window [%s, %s]", raw.Name, ver, kit.SchemaFloor(), kit.LatestSchemaVersion()))
		}
		prev = ver
		out = append(out, migration{Version: ver, Name: raw.Name, TouchesHost: raw.TouchesHost, Ops: raw.Ops, Apply: raw.Apply})
	}
	return out
}

// runMigrations brings the project (and, unless projectOnly, the per-host overlay)
// up to the head schema. Returns whether anything changed.
func runMigrations(ctx *MigrateContext, projectOnly bool) (bool, error) {
	if ctx == nil {
		return false, errors.New("migrate: nil context")
	}
	out := ctx.Out
	if out == nil {
		out = io.Discard
	}
	head := kit.LatestSchemaVersion()
	floor := kit.SchemaFloor()

	rootPath := filepath.Join(ctx.Dir, kit.UnifiedFileName)
	data, err := os.ReadFile(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "no %s in %s — nothing to migrate\n", kit.UnifiedFileName, ctx.Dir)
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", rootPath, err)
	}
	ver := kit.FirstYAMLVersionLine(data)
	fileVer, ok := kit.ParseCalVer(ver)
	switch {
	case ok && head.Less(fileVer):
		return false, fmt.Errorf(
			"%s: schema %s is newer than this charly supports (max %s) — update charly (reinstall the latest opencharly package, or `task build:charly` from a fresh checkout)",
			rootPath, ver, head)
	case ok && fileVer == head:
		fmt.Fprintf(out, "already at schema %s; nothing to migrate\n", head)
		return false, nil
	case !ok || fileVer.Less(floor):
		return false, fmt.Errorf(
			"%s: schema %q predates the supported floor %s and cannot be migrated — the historical migration chain was removed at the %s baseline reset. Re-author this config against the current schema (a current config carries `version: %s`).",
			rootPath, ver, floor, head, head)
	}

	// floor <= fileVer < head: apply the table's newer steps, then re-stamp to head.
	var applied []string
	for _, m := range migrationTable {
		if !fileVer.Less(m.Version) {
			continue // already covered by the file's current version
		}
		transform, terr := buildTransform(m)
		if terr != nil {
			return len(applied) > 0, terr
		}
		files, ferr := runDocMigration(ctx.Dir, ctx.DryRun, kit.OpUnifyCandidateFiles, transform)
		if ferr != nil {
			return len(applied) > 0, ferr
		}
		hostChanged := false
		if m.TouchesHost && !projectOnly {
			hostChanged, ferr = migrateHostOverlayDoc(ctx, transform)
			if ferr != nil {
				return len(applied) > 0, ferr
			}
		}
		if len(files) > 0 || hostChanged {
			applied = append(applied, m.Name)
			fmt.Fprintf(out, "applied %s (schema %s)\n", m.Name, m.Version)
		}
	}
	stamped, serr := universalStamp(ctx, head, projectOnly)
	if serr != nil {
		return len(applied) > 0, serr
	}
	changed := len(applied) > 0 || len(stamped) > 0
	if changed {
		fmt.Fprintf(out, "migrated to schema %s\n", head)
	} else {
		fmt.Fprintf(out, "nothing to migrate (already at schema %s)\n", head)
	}
	return changed, nil
}

// buildTransform returns the per-document transform for a step: the generic
// op-walker for a declarative step, or the named Go hook for an `apply:` step.
func buildTransform(m migration) (func(*yaml.Node) bool, error) {
	if m.Apply != "" {
		hook, ok := goHooks[m.Apply]
		if !ok {
			return nil, fmt.Errorf("migration %q: unknown Go hook %q", m.Name, m.Apply)
		}
		return hook, nil
	}
	ops := m.Ops
	return func(doc *yaml.Node) bool {
		root := kit.MappingRoot(doc)
		if root == nil {
			return false
		}
		changed := false
		for _, op := range ops {
			if applyOp(root, op) {
				changed = true
			}
		}
		return changed
	}, nil
}

// applyOp applies one declarative op to the target mappings selected by its scope /
// under_kind. Returns whether anything changed.
func applyOp(root *yaml.Node, op migrationOp) bool {
	changed := false
	for _, m := range opTargetMappings(root, op) {
		if applyOpToMapping(m, op) {
			changed = true
		}
	}
	return changed
}

// opTargetMappings selects the mapping nodes an op applies to:
//   - under_kind K: every mapping that is (or is nested within) an entity value
//     carrying a direct `K:` discriminator key;
//   - scope root: the document root mapping only;
//   - scope any (default): every mapping in the tree.
func opTargetMappings(root *yaml.Node, op migrationOp) []*yaml.Node {
	if op.UnderKind != "" {
		var out []*yaml.Node
		var rec func(n *yaml.Node, inside bool)
		rec = func(n *yaml.Node, inside bool) {
			if n == nil {
				return
			}
			switch n.Kind {
			case yaml.MappingNode:
				here := inside || mappingHasKey(n, op.UnderKind)
				if here {
					out = append(out, n)
				}
				for i := 0; i+1 < len(n.Content); i += 2 {
					rec(n.Content[i+1], here)
				}
			case yaml.DocumentNode, yaml.SequenceNode:
				for _, c := range n.Content {
					rec(c, inside)
				}
			}
		}
		rec(root, false)
		return out
	}
	if op.Scope == "root" {
		return []*yaml.Node{root}
	}
	return allMappings(root)
}

// applyOpToMapping applies an op to a single mapping node (comment-preserving).
func applyOpToMapping(m *yaml.Node, op migrationOp) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	switch op.Op {
	case "rename_key":
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == op.From {
				m.Content[i].Value = op.To
				return true
			}
		}
	case "delete_key":
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == op.Key {
				// carry the deleted key's head comment onto the following key so a
				// section banner is not lost.
				if i+2 < len(m.Content) && m.Content[i].HeadComment != "" && m.Content[i+2].HeadComment == "" {
					m.Content[i+2].HeadComment = m.Content[i].HeadComment
				}
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				return true
			}
		}
	case "remap_scalar":
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == op.Key {
				if v := m.Content[i+1]; v.Kind == yaml.ScalarNode && v.Value == op.From {
					v.Value = op.To
					return true
				}
			}
		}
	case "move_key":
		from := childMapping(m, op.FromParent)
		to := childMapping(m, op.ToParent)
		if from == nil || to == nil {
			return false
		}
		for i := 0; i+1 < len(from.Content); i += 2 {
			if from.Content[i].Value == op.Key {
				k, v := from.Content[i], from.Content[i+1]
				from.Content = append(from.Content[:i:i], from.Content[i+2:]...)
				to.Content = append(to.Content, k, v)
				return true
			}
		}
	}
	return false
}

// allMappings returns every mapping node in the tree (document root first).
func allMappings(n *yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	var rec func(*yaml.Node)
	rec = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			out = append(out, n)
		}
		for _, c := range n.Content {
			rec(c)
		}
	}
	rec(n)
	return out
}

// mappingHasKey reports whether mapping m has a direct child key named key.
func mappingHasKey(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}

// childMapping returns the mapping value of key in m, or nil.
func childMapping(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key && m.Content[i+1].Kind == yaml.MappingNode {
			return m.Content[i+1]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// universal version stamp
// ---------------------------------------------------------------------------

// universalStampFiles are the project files carrying a top-level schema `version:`
// stamp. In the single-filename world that is charly.yml alone (box/candy manifests
// carry a per-ENTITY version nested under their kind value, never a top-level stamp).
var universalStampFiles = []string{kit.UnifiedFileName}

// universalStamp rewrites the top-level `version:` of every stamped project file
// (and, unless projectOnly, the per-host overlay) to head. Returns changed paths.
func universalStamp(ctx *MigrateContext, head kit.CalVer, projectOnly bool) ([]string, error) {
	var changed []string
	for _, name := range universalStampFiles {
		did, err := stampVersionField(filepath.Join(ctx.Dir, name), head.String(), ctx.DryRun)
		if err != nil {
			return changed, err
		}
		if did {
			changed = append(changed, name)
		}
	}
	if !projectOnly && ctx.HostDeployPath != "" {
		did, err := stampVersionField(ctx.HostDeployPath, head.String(), ctx.DryRun)
		if err != nil {
			return changed, err
		}
		if did {
			changed = append(changed, ctx.HostDeployPath)
		}
	}
	return changed, nil
}

// stampVersionField rewrites the first top-level `version:` line of one file to
// `version: <want>`, preserving any trailing comment. Returns (changed, err);
// changed is false when the file is absent, has no top-level version: key, or is
// already at want. A <path>.bak.<unix-ts> rollback is written before any rewrite.
func stampVersionField(path, want string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	idx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "version:") {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false, nil // no top-level version: key
	}
	newLine := "version: " + want
	if h := strings.Index(lines[idx], "#"); h >= 0 {
		newLine += "  " + strings.TrimSpace(lines[idx][h:])
	}
	if lines[idx] == newLine {
		return false, nil // already stamped
	}
	if dryRun {
		return true, nil
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		return false, fmt.Errorf("writing backup %s: %w", backup, err)
	}
	lines[idx] = newLine
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// generic file-walk drivers (relocated verbatim from the retired candy — R3)
// ---------------------------------------------------------------------------

// runDocMigration scans candidateFiles(dir), decodes each as a YAML multi-document
// stream, applies transform to every document, and — when any document changed —
// re-encodes the whole stream (4-space indent) and writes it back (0o644) unless
// dryRun. Returns the rewritten paths; unreadable files are skipped.
func runDocMigration(dir string, dryRun bool, candidateFiles func(string) []string, transform func(*yaml.Node) bool) ([]string, error) {
	var rewritten []string
	for _, path := range candidateFiles(dir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable siblings; don't abort
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var docs []*yaml.Node
		changed := false
		for {
			var doc yaml.Node
			if derr := dec.Decode(&doc); derr != nil {
				break
			}
			d := doc
			if transform(&d) {
				changed = true
			}
			docs = append(docs, &d)
		}
		if !changed {
			continue
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(4)
		for _, d := range docs {
			if eerr := enc.Encode(d); eerr != nil {
				return rewritten, fmt.Errorf("encoding %s: %w", path, eerr)
			}
		}
		_ = enc.Close()
		if !dryRun {
			if werr := os.WriteFile(path, buf.Bytes(), 0o644); werr != nil {
				return rewritten, fmt.Errorf("writing %s: %w", path, werr)
			}
		}
		rewritten = append(rewritten, path)
	}
	return rewritten, nil
}

// migrateHostOverlayDoc applies a document transform to the per-host deploy overlay
// (ctx.HostDeployPath) in addition to the project files. Gated on a non-empty
// HostDeployPath, so the project-only runner (remote-cache auto-migration) never
// touches the user's per-host state. Returns whether the overlay changed.
func migrateHostOverlayDoc(ctx *MigrateContext, transform func(*yaml.Node) bool) (bool, error) {
	if ctx.HostDeployPath == "" {
		return false, nil
	}
	return rewriteDocFile(ctx.HostDeployPath, ctx.DryRun, transform)
}

// rewriteDocFile reads path, decodes ONE YAML document, applies transform, and —
// when it changed — re-encodes (4-space indent) and writes it back (0644) unless
// dryRun, after saving a .bak.<unix-ts> copy. A missing/unparseable file is a no-op.
func rewriteDocFile(path string, dryRun bool, transform func(*yaml.Node) bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil
	}
	if !transform(&doc) {
		return false, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return false, err
	}
	_ = enc.Close()
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	_ = os.WriteFile(bak, data, 0644)
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return false, err
	}
	return true, nil
}
