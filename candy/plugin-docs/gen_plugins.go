package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pluginEntity is a candy carrying a `plugin:` block — one of the 86 plugin candies.
type pluginEntity struct {
	entity
	CompiledIn bool
	Schemas    []schemaFile
}

type schemaFile struct {
	Name string // file name, e.g. "adb.cue"
	Body string
}

// Providers returns the declared "<class>:<word>" capability strings.
func (p pluginEntity) Providers() []string {
	if p.Candy == nil || p.Candy.Plugin == nil {
		return nil
	}
	out := make([]string, 0, len(p.Candy.Plugin.Providers))
	for _, c := range p.Candy.Plugin.Providers {
		if s := strings.TrimSpace(string(c)); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Placement describes where this plugin's providers actually run. It is COMPUTED from
// compiled_plugins membership rather than transcribed into prose, so a plugin's documented
// placement cannot drift when that list changes.
func (p pluginEntity) Placement() string {
	if p.CompiledIn {
		return "compiled-in (in-process)"
	}
	return "runtime (out-of-process over gRPC)"
}

// PlacementNote explains the consequence of the placement for a reader.
func (p pluginEntity) PlacementNote() string {
	if p.CompiledIn {
		return "This plugin is listed in `charly/charly.yml`'s `compiled_plugins:`, so its providers are compiled into the `charly` binary and register in-process."
	}
	return "This plugin is **not** listed in `charly/charly.yml`'s `compiled_plugins:`. It is not part of the shipped binary: charly builds and loads it out-of-process over gRPC when a plan references one of its words (the coexist path)."
}

// collectPlugins gathers every plugin candy together with its placement and CUE schemas.
func collectPlugins(root string, entities []entity, compiled map[string]bool) ([]pluginEntity, error) {
	var out []pluginEntity
	for _, e := range entities {
		if e.IsBox || e.Candy == nil || e.Candy.Plugin == nil {
			continue
		}
		pe := pluginEntity{entity: e, CompiledIn: compiled[e.Name]}
		schemaDir := filepath.Join(root, e.Dir, "schema")
		if e.Namespace != "" {
			schemaDir = filepath.Join(root, "box", e.Namespace, e.Dir, "schema")
		}
		ents, err := os.ReadDir(schemaDir)
		if err == nil {
			for _, de := range ents {
				if de.IsDir() || !strings.HasSuffix(de.Name(), ".cue") {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(schemaDir, de.Name()))
				if err != nil {
					return nil, fmt.Errorf("read schema %s: %w", de.Name(), err)
				}
				pe.Schemas = append(pe.Schemas, schemaFile{Name: de.Name(), Body: string(raw)})
			}
			sort.Slice(pe.Schemas, func(i, j int) bool { return pe.Schemas[i].Name < pe.Schemas[j].Name })
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", schemaDir, err)
		}
		out = append(out, pe)
	}
	return out, nil
}

func pluginPagePath(p pluginEntity) string {
	return "reference/plugin/" + p.PathSegment() + ".md"
}

func pluginSitePath(p pluginEntity) string {
	return "/reference/plugin/" + p.PathSegment() + "/"
}

// generatePlugins emits one page per plugin candy plus the provider cross-index.
func generatePlugins(outRoot string, plugins []pluginEntity) (int, error) {
	count := 0
	for _, p := range plugins {
		var b strings.Builder

		fmt.Fprintf(&b, "| | |\n|---|---|\n")
		fmt.Fprintf(&b, "| **Placement** | %s |\n", p.Placement())
		if p.Candy.Plugin.Source != "" {
			fmt.Fprintf(&b, "| **Source** | `%s` |\n", p.Candy.Plugin.Source)
		}
		if v := p.Version(); v != "" {
			fmt.Fprintf(&b, "| **Version** | `%s` |\n", v)
		}
		fmt.Fprintf(&b, "| **Candy** | `%s` |\n\n", p.Name)

		b.WriteString(p.PlacementNote())
		b.WriteString("\n\n## Providers\n\n")
		provs := p.Providers()
		if len(provs) == 0 {
			b.WriteString("This plugin declares no provider words.\n")
		} else {
			b.WriteString("The reserved words this plugin serves:\n\n")
			for _, c := range provs {
				class, word, _ := strings.Cut(c, ":")
				fmt.Fprintf(&b, "- **`%s`** — %s class\n", word, class)
			}
		}

		if d := strings.TrimSpace(p.Description()); d != "" {
			b.WriteString("\n## What it does\n\n")
			b.WriteString(d)
			b.WriteString("\n")
		}

		if len(p.Schemas) > 0 {
			b.WriteString("\n## Parameter schema\n\n")
			b.WriteString("The CUE schema below is the authoritative grammar for this plugin's input. " +
				"It is the same single source that generates the plugin's Go parameter types and answers " +
				"the runtime `Describe` RPC, so this page cannot disagree with either.\n\n")
			for _, s := range p.Schemas {
				fmt.Fprintf(&b, "### `schema/%s`\n\n%s\n\n", s.Name, fence("cue", s.Body))
			}
		}

		fmt.Fprintf(&b, "\n---\n\nSee also the [candy reference](%s) for this candy's install surface.\n",
			candySitePathFor(p.PathSegment()))

		if err := (page{
			Path:        pluginPagePath(p),
			Title:       p.Name,
			Description: p.Description(),
			Body:        b.String(),
		}).write(outRoot); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

// generateProviderIndex inverts the per-plugin declarations into one lookup: every reserved word
// mapped to the plugin that owns it. Nothing in the repo answers "what implements `cdp:`?" today.
func generateProviderIndex(outRoot string, plugins []pluginEntity) (int, error) {
	type row struct{ class, word, plugin, page string }
	var rows []row
	for _, p := range plugins {
		for _, c := range p.Providers() {
			class, word, ok := strings.Cut(c, ":")
			if !ok {
				continue
			}
			rows = append(rows, row{class: class, word: word, plugin: p.Name, page: pluginSitePath(p)})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].class != rows[j].class {
			return rows[i].class < rows[j].class
		}
		return rows[i].word < rows[j].word
	})

	// The census is computed here, never transcribed: the three numbers are derived from the same
	// plugins slice the page is built from, so a catalog change re-derives them on the next run.
	// A hand-written count in prose elsewhere is what this page exists to replace.
	compiled := 0
	for _, p := range plugins {
		if p.CompiledIn {
			compiled++
		}
	}
	classCount := map[string]int{}
	for _, r := range rows {
		classCount[r.class]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%d words across %d plugin candies, %d compiled into the binary.** "+
		"Every reserved word charly can dispatch, and the plugin candy that serves it. "+
		"A word's class tells you where it appears: a `verb` in a plan step, a `kind` as an entity's "+
		"kind key, a `command` as a `charly <word>` subcommand, and so on.\n\n", len(rows), len(plugins), compiled)
	current := ""
	for _, r := range rows {
		if r.class != current {
			current = r.class
			fmt.Fprintf(&b, "\n## `%s` — %d words\n\n| Word | Served by |\n|---|---|\n", current, classCount[current])
		}
		fmt.Fprintf(&b, "| `%s` | [%s](%s) |\n", r.word, r.plugin, r.page)
	}

	return len(rows), (page{
		Path:        "reference/providers.md",
		Title:       "Provider index",
		Description: "Every reserved word — verb, kind, deploy target, step, builder, command — mapped to the plugin candy that serves it.",
		Body:        b.String(),
	}).write(outRoot)
}
