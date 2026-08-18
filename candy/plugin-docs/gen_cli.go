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
		owners []pluginEntity
	}
	// Group by WORD, not one entry per provider. A word can legitimately be served by
	// more than one plugin — `feature` is both a top-level command (candy/plugin-feature)
	// and a box-nested one (candy/plugin-box), and both declarations are true. Keyed by
	// word alone with one page written per PROVIDER, the second write silently overwrote
	// the first: 56 emitted, 55 written, and `box feature` ended up with no page while the
	// provider index still counted 56. Silent overwrite is the bug — this file's
	// cross-reference gate already fails closed, and the page-write path must not fail
	// open beside it.
	byWord := map[string][]pluginEntity{}
	for _, p := range plugins {
		for _, prov := range p.Providers() {
			class, word, ok := strings.Cut(prov, ":")
			if !ok || class != "command" {
				continue
			}
			byWord[word] = append(byWord[word], p)
		}
	}
	var cmds []cmd
	for word, owners := range byWord {
		sort.Slice(owners, func(i, j int) bool { return owners[i].Name < owners[j].Name })
		cmds = append(cmds, cmd{word: word, owners: owners})
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].word < cmds[j].word })

	for _, c := range cmds {
		var b strings.Builder
		// Key the rows by OWNER, not by field name. With one row per (field, owner) the
		// keys repeat with no grouping, and while both Placement values may coincide, the
		// Version values do not — so a reader cannot tell which version belongs to which
		// candy. Single-owner pages keep the plain field names they always had.
		b.WriteString("| | |\n|---|---|\n")
		for _, o := range c.owners {
			label := func(field string) string {
				if len(c.owners) == 1 {
					return field
				}
				return o.Name + " — " + field
			}
			fmt.Fprintf(&b, "| **%s** | [%s](%s) |\n", label("Served by"), o.Name, pluginSitePath(o))
			fmt.Fprintf(&b, "| **%s** | %s |\n", label("Placement"), o.Placement())
			if v := o.Version(); v != "" {
				fmt.Fprintf(&b, "| **%s** | `%s` |\n", label("Version"), v)
			}
		}
		b.WriteString("\n")

		if len(c.owners) == 1 {
			fmt.Fprintf(&b, "`%s` is a command word served by the `%s` plugin candy. %s\n\n",
				c.word, c.owners[0].Name, c.owners[0].PlacementNote())
		} else {
			names := make([]string, 0, len(c.owners))
			for _, o := range c.owners {
				names = append(names, "`"+o.Name+"`")
			}
			fmt.Fprintf(&b, "`%s` is a command word served by %d plugin candies — %s — "+
				"at different points in the command tree. Both are real invocations; "+
				"`charly --help` prints which is which.\n\n",
				c.word, len(c.owners), strings.Join(names, " and "))
		}

		for _, o := range c.owners {
			if d := strings.TrimSpace(o.Description()); d != "" {
				if len(c.owners) == 1 {
					b.WriteString("## About the plugin that serves it\n\n")
				} else {
					fmt.Fprintf(&b, "## About `%s`, which serves it\n\n", o.Name)
				}
				b.WriteString(d)
				b.WriteString("\n\n")
			}
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
		if len(c.owners) == 1 {
			fmt.Fprintf(&b, "`charly --help` prints the command tree, including where `%s` "+
				"is invoked and under which parent.\n", c.word)
		} else {
			fmt.Fprintf(&b, "`charly --help` prints the command tree, including where each "+
				"`%s` is invoked and under which parent.\n", c.word)
		}

		if err := (page{
			Path:        "reference/cli/" + c.word + ".md",
			Title:       c.word,
			Description: cliPageDescription(c.word, c.owners),
			Body:        b.String(),
		}).write(outRoot); err != nil {
			return 0, err
		}
	}
	return len(cmds), nil
}

// cliPageDescription renders the page subtitle, which is ALSO the search-result text and is
// therefore read before the body. It must not name one owner when a word has several: the
// definite article ("served by THE x plugin candy") asserts exclusivity, and a per-page
// constant with a single name substituted only survives contact with single-server pages.
// Choosing WHICH single owner to name is the same clobbering the grouped body exists to
// prevent, one field up — the previous form did exactly that, and a fix that moved the name
// from one owner to the other would have preserved the defect while looking like a cure.
func cliPageDescription(word string, owners []pluginEntity) string {
	if len(owners) == 1 {
		return fmt.Sprintf("The %s command word, served by the %s plugin candy.", word, owners[0].Name)
	}
	names := make([]string, 0, len(owners))
	for _, o := range owners {
		names = append(names, o.Name)
	}
	return fmt.Sprintf("The %s command word, served by %d plugin candies: %s.",
		word, len(owners), strings.Join(names, " and "))
}
