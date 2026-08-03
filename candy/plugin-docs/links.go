package docs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// skillRefPattern matches a harness skill reference — `/charly-<plugin>:<skill>` — with the two
// guards the corpus actually needs (both established by measuring all 3788 occurrences):
//
//  1. The skill part must START WITH A LETTER. Without this, host:port strings match: the corpus
//     contains `redis://charly-redis:6379`, `http://charly-jupyter:8888`,
//     `http://charly-ollama:11434` and `charly-valkey:6379`, whose "skill" parts are numeric.
//  2. The reference must be preceded by a WORD BOUNDARY — not `/` and not a word character.
//     Both exclusions are load-bearing and were each caught by a real corpus case: `/` excludes
//     URL authorities (`redis://charly-redis:…`), and the word-character exclusion excludes
//     container image tags, where the reference is the tail of a longer path
//     (`localhost/charly-selkies-kde:latest`, `ghcr.io/opencharly/charly-…:…`). Go's regexp has
//     no lookbehind, so the optional preceding byte is captured as group 1 and checked in the
//     replacer.
//
// Group 2 and 5 capture the optional surrounding backticks: 3745 of the 3788 occurrences are
// inside a code span, and a link must WRAP the span (`[`/charly-x:y`](url)`) rather than be
// injected inside it, where markdown would render the link syntax literally.
// Group 1 excludes the backtick deliberately: a plain `.?` is greedy enough to swallow the
// OPENING backtick of a code span when the reference starts the string, which then emits the
// link wrapped in backticks (rendering the markdown literally) instead of wrapping the span.
var skillRefPattern = regexp.MustCompile("(?s)([^`]?)(`?)/charly-([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)(`?)")

// referencePathPattern matches a skill's backticked pointer to one of its own split files, e.g.
// `references/beds-and-r10.md`. The corpus links these as backticked PATHS exclusively — there
// are zero markdown-link forms — so one uniform rewrite covers them.
var referencePathPattern = regexp.MustCompile("`references/([a-z0-9-]+)\\.md`")

// danglingRef is a reference that resolves to nothing. It is an ERROR, never a silent dead link:
// the corpus is clean today (all 256 distinct targets resolve), so failing closed keeps it clean
// and turns the docs build into a corpus-wide integrity check the repo otherwise lacks.
type danglingRef struct {
	Source string // the file the bad reference was found in
	Ref    string // the reference text
}

// linkResolver answers whether a reference target exists and where its page lives.
type linkResolver struct {
	// skillPages maps "<plugin>:<skill>" to its site path.
	skillPages map[string]string
	// refPages maps "<plugin>/<skill>/<reference-stem>" to its site path.
	refPages map[string]string
}

// rewrite converts every harness reference in body into a working site link, collecting any that
// resolve to nothing. plugin and skill identify the page being rendered, which is what lets a
// relative `references/<file>.md` pointer resolve to that skill's own child page.
func (lr linkResolver) rewrite(body, plugin, skill, source string) (string, []danglingRef) {
	var dangling []danglingRef

	out := referencePathPattern.ReplaceAllStringFunc(body, func(m string) string {
		sub := referencePathPattern.FindStringSubmatch(m)
		stem := sub[1]
		key := plugin + "/" + skill + "/" + stem
		page, ok := lr.refPages[key]
		if !ok {
			dangling = append(dangling, danglingRef{Source: source, Ref: "references/" + stem + ".md"})
			return m
		}
		return fmt.Sprintf("[`references/%s.md`](%s)", stem, page)
	})

	out = skillRefPattern.ReplaceAllStringFunc(out, func(m string) string {
		sub := skillRefPattern.FindStringSubmatch(m)
		prev, openTick, refPlugin, refSkill, closeTick := sub[1], sub[2], sub[3], sub[4], sub[5]
		// Guard 2: only a word-boundary-preceded match is a skill reference. A `/` means a URL
		// authority (`redis://charly-redis:…`); a word character means the tail of a longer
		// path, i.e. a container image tag (`localhost/charly-selkies-kde:latest`). Leave both
		// exactly as authored.
		if !isRefBoundary(prev) {
			return m
		}
		key := refPlugin + ":" + refSkill
		page, ok := lr.skillPages[key]
		if !ok {
			dangling = append(dangling, danglingRef{Source: source, Ref: "/charly-" + key})
			return m
		}
		// Only treat the backticks as a code span when they are balanced around the reference;
		// a lone tick belongs to neighbouring markup and must be preserved in place.
		if openTick == "`" && closeTick == "`" {
			return fmt.Sprintf("%s[`/charly-%s`](%s)", prev, key, page)
		}
		return fmt.Sprintf("%s%s[/charly-%s](%s)%s", prev, openTick, key, page, closeTick)
	})

	return out, dangling
}

// danglingError renders collected dangling references as one actionable build failure.
func danglingError(all []danglingRef) error {
	if len(all) == 0 {
		return nil
	}
	byRef := map[string][]string{}
	for _, d := range all {
		byRef[d.Ref] = append(byRef[d.Ref], d.Source)
	}
	keys := make([]string, 0, len(byRef))
	for k := range byRef {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d unresolvable cross-reference(s) in the skill corpus:\n", len(byRef))
	for _, k := range keys {
		srcs := byRef[k]
		sort.Strings(srcs)
		fmt.Fprintf(&b, "  %s\n    referenced from: %s\n", k, strings.Join(dedupe(srcs), ", "))
	}
	b.WriteString("every reference must resolve to a defined skill or reference file")
	return fmt.Errorf("%s", b.String())
}

// isRefBoundary reports whether the byte preceding a candidate reference lets it BE a reference.
// Empty means start-of-string. Anything alphanumeric (or `/`, `.`, `-`, `_`) means the match is
// the tail of a longer token — a path, a URL, or an image tag — and not a skill reference.
func isRefBoundary(prev string) bool {
	if prev == "" {
		return true
	}
	c := prev[0]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	case c == '/', c == '.', c == '-', c == '_':
		return false
	}
	return true
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
