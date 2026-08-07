package marketplace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// emit_dispatcher.go — the R0 Skill Dispatcher generation. The CLAUDE.md/AGENTS.md dispatcher
// table lives between `<!-- BEGIN GENERATED SKILL DISPATCHER -->` / `<!-- END GENERATED SKILL
// DISPATCHER -->` markers; this replaces the section with a table built from every skill's
// declared `triggers:` (one row per trigger phrase → the skill's invocation). A file without the
// markers is left untouched (the markers land with the U6 dispatcher cutover).

const (
	dispatcherBegin = "<!-- BEGIN GENERATED SKILL DISPATCHER -->"
	dispatcherEnd   = "<!-- END GENERATED SKILL DISPATCHER -->"
)

func emitDispatcher(em emissions, root string, families []family) error {
	table := buildDispatcherTable(families)
	for _, rel := range []string{"CLAUDE.md", "AGENTS.md"} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		s := string(raw)
		bi := strings.Index(s, dispatcherBegin)
		ei := strings.Index(s, dispatcherEnd)
		if bi < 0 || ei < 0 || ei < bi {
			continue // no markers → not a generated-dispatcher file; leave untouched
		}
		replacement := dispatcherBegin + "\n" + table + dispatcherEnd
		s = s[:bi] + replacement + s[ei+len(dispatcherEnd):]
		em[rel] = []byte(s)
	}
	return nil
}

// buildDispatcherTable renders the trigger → skill table.
func buildDispatcherTable(families []family) string {
	type row struct{ trigger, skill string }
	var rows []row
	for _, f := range families {
		for _, s := range f.Skills {
			if len(s.Triggers) == 0 {
				continue // agents with triggers (e.g. root-cause-analyzer) ARE dispatcher rows
			}
			inv := "/charly-" + f.Name + ":" + s.Name
			for _, trig := range s.Triggers {
				rows = append(rows, row{trigger: trig, skill: inv})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].trigger != rows[j].trigger {
			return rows[i].trigger < rows[j].trigger
		}
		return rows[i].skill < rows[j].skill
	})
	var b strings.Builder
	b.WriteString("| Trigger (what the user said or you're about to do) | Skill to load |\n")
	b.WriteString("|---|---|\n")
	for _, r := range rows {
		b.WriteString("| " + r.trigger + " | `" + r.skill + "` |\n")
	}
	return b.String()
}
