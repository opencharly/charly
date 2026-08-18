package docs

import (
	"fmt"
	"sort"
	"strings"
)

// generateCLI emits one page per COMMAND word charly can dispatch.
//
// The source is declarative: every `command:<word>` in some plugin candy's `plugin.providers`.
// That is deliberately NOT the charly binary's own help output. The host renders every dynamic
// command word with a generic stub description, intercepts the depth-1 `--help` itself, and the
// plugin-served help arrives in at least three mutually incompatible formats (kong trees, custom
// usage lines, and parse errors) with no machine-readable dump anywhere — so scraping it would
// be a fragile shim, and a shim that silently rots as plugins change.
//
// The trade-off is honest: charly's command surface is plugin-served almost end to end (of the
// top-level words, only the core spine `box`, `version` and `reap-orphans` are not
// `command:` providers), and command PARENTHOOD is a Go method (CommandParent()) rather than a
// manifest field, so these pages name the word and its owning plugin without asserting where it
// nests. The narrative CLI guide — hand-written, where prose belongs — covers the spine and the
// nesting; nothing about either is transcribed into generated output, so nothing here can drift.
func generateCLI(outRoot string, plugins []pluginEntity) (int, error) {

	type cmd struct {
		word   string
		plugin pluginEntity
	}
	var cmds []cmd
	for _, p := range plugins {
		for _, prov := range p.Providers() {
			class, word, ok := strings.Cut(prov, ":")
			if !ok || class != "command" {
				continue
			}
			cmds = append(cmds, cmd{word: word, plugin: p})
		}
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].word < cmds[j].word })

	for _, c := range cmds {
		var b strings.Builder
		b.WriteString("| | |\n|---|---|\n")
		fmt.Fprintf(&b, "| **Served by** | [%s](%s) |\n", c.plugin.Name, pluginSitePath(c.plugin))
		fmt.Fprintf(&b, "| **Placement** | %s |\n", c.plugin.Placement())
		if v := c.plugin.Version(); v != "" {
			fmt.Fprintf(&b, "| **Version** | `%s` |\n", v)
		}
		b.WriteString("\n")

		fmt.Fprintf(&b, "`%s` is a command word served by the `%s` plugin candy. %s\n\n",
			c.word, c.plugin.Name, c.plugin.PlacementNote())

		if d := strings.TrimSpace(c.plugin.Description()); d != "" {
			b.WriteString("## About the plugin that serves it\n\n")
			b.WriteString(d)
			b.WriteString("\n\n")
		}

		// NOT `charly <word> --help`. Most command words are NESTED (twelve live under
		// `box` alone), so that spelling is wrong for the majority and — worse — wrong
		// SILENTLY: an unrecognised leading word makes charly print ROOT usage and exit 0,
		// so a reader following it sees output and no error. Parenthood is a Go method
		// (CommandParent()), and `charly __cli-model` only synthesises leaves for words
		// that declare Subcommands (95 leaves; `box.list.*` is there, `box.load` is not),
		// so the generator cannot resolve the parent from any machine-readable source.
		// It therefore points at the command tree, which is both runnable and authoritative,
		// instead of fabricating an invocation (R4a: never document a command that fails).
		// It also asserts NOTHING about this word's nesting: the plugin description rendered
		// above often states it, and a generic "may be top-level or nested" line contradicted
		// that on all twelve box pages.
		fmt.Fprintf(&b, "`charly --help` prints the command tree, including where `%s` "+
			"is invoked and under which parent.\n", c.word)

		if err := (page{
			Path:        "reference/cli/" + c.word + ".md",
			Title:       c.word,
			Description: fmt.Sprintf("The %s command word, served by the %s plugin candy.", c.word, c.plugin.Name),
			Body:        b.String(),
		}).write(outRoot); err != nil {
			return 0, err
		}
	}
	return len(cmds), nil
}
