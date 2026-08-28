package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plugin word that resolves to no provider has FOUR distinct causes, and the message used to
// name none of them: it reported only "no provider registered for plugin verb %q", which reads
// as "the plugin is broken" when the usual cause is "charly never looked where it is" or
// "charly chose not to load it".
//
// Each case below produces the SAME string without the diagnostic, so each fails on the old
// behaviour. Diagnosing exactly this on a live bed previously cost an eight-step RCA and four
// wrong hypotheses.
func TestExplainUnresolvedPluginWordNamesTheCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup returns nothing; it prepares env + package state for the case.
		setup func(t *testing.T)
		want  []string
		// deny asserts strings that must NOT appear (guards against a message that
		// claims the wrong cause).
		deny []string
	}{
		{
			name: "declared by a project candy that perf-scoping dropped",
			setup: func(t *testing.T) {
				recordScopedOutPlugin("plugin-wl", []string{"verb:wl"})
			},
			want: []string{
				`plugin-wl`,
				`perf-scoping`,
				`add_candy`,
			},
			// The plugin is fine and was never looked for on disk; saying the search
			// path lacks it would send the reader to the wrong place entirely.
			deny: []string{"declared by no .providers manifest"},
		},
		{
			name: "in a manifest under a directory that is NOT on the search path",
			setup: func(t *testing.T) {
				onPath := t.TempDir()
				offPath := t.TempDir()
				writeProviders(t, offPath, "plugin-wl", "verb:wl")
				t.Setenv("CHARLY_PLUGIN_DIR", onPath)
				t.Setenv("CHARLY_PLUGIN_ONLY", "1")
			},
			want: []string{
				`declared by no .providers manifest`,
				`CHARLY_PLUGIN_DIR`,
			},
		},
		{
			name: "declared in a manifest whose binary is missing",
			setup: func(t *testing.T) {
				dir := t.TempDir()
				writeProviders(t, dir, "plugin-wl", "verb:wl")
				t.Setenv("CHARLY_PLUGIN_DIR", dir)
				t.Setenv("CHARLY_PLUGIN_ONLY", "1")
			},
			want: []string{
				`binary`,
				`is missing`,
				`verb:wl -> plugin-wl`,
			},
			deny: []string{"declared by no .providers manifest"},
		},
		{
			name: "empty search path",
			setup: func(t *testing.T) {
				t.Setenv("CHARLY_PLUGIN_DIR", "")
				t.Setenv("CHARLY_PLUGIN_ONLY", "1")
			},
			want: []string{
				`search path is EMPTY`,
				`CHARLY_PLUGIN_ONLY`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// pluginScopedOut is package state; keep cases independent.
			saved := pluginScopedOut
			pluginScopedOut = map[string]string{}
			t.Cleanup(func() { pluginScopedOut = saved })

			tc.setup(t)
			got := explainUnresolvedPluginWord(ClassVerb, "wl")

			// The symptom must still be reported — callers and beds match on it.
			if !strings.Contains(got, `no provider registered for plugin verb "wl"`) {
				t.Errorf("lost the original symptom line:\n%s", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("message does not name the cause: missing %q\n--- got ---\n%s", w, got)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(got, d) {
					t.Errorf("message claims the WRONG cause: contains %q\n--- got ---\n%s", d, got)
				}
			}
		})
	}
}

// The four causes must not render identically — that indistinguishability IS the defect.
func TestExplainUnresolvedPluginWordDistinguishesCauses(t *testing.T) {
	saved := pluginScopedOut
	t.Cleanup(func() { pluginScopedOut = saved })

	seen := map[string]string{}
	render := func(name string, prep func(t *testing.T)) {
		pluginScopedOut = map[string]string{}
		prep(t)
		got := explainUnresolvedPluginWord(ClassVerb, "wl")
		if prev, dup := seen[got]; dup {
			t.Errorf("%q and %q render the SAME message — the caller cannot tell them apart:\n%s",
				name, prev, got)
		}
		seen[got] = name
	}

	dirWithManifest := t.TempDir()
	writeProviders(t, dirWithManifest, "plugin-wl", "verb:wl")
	empty := t.TempDir()

	render("scoped-out", func(t *testing.T) {
		t.Setenv("CHARLY_PLUGIN_DIR", empty)
		t.Setenv("CHARLY_PLUGIN_ONLY", "1")
		recordScopedOutPlugin("plugin-wl", []string{"verb:wl"})
	})
	render("not-on-path", func(t *testing.T) {
		t.Setenv("CHARLY_PLUGIN_DIR", empty)
		t.Setenv("CHARLY_PLUGIN_ONLY", "1")
	})
	render("manifest-without-binary", func(t *testing.T) {
		t.Setenv("CHARLY_PLUGIN_DIR", dirWithManifest)
		t.Setenv("CHARLY_PLUGIN_ONLY", "1")
	})
	render("empty-path", func(t *testing.T) {
		t.Setenv("CHARLY_PLUGIN_DIR", "")
		t.Setenv("CHARLY_PLUGIN_ONLY", "1")
	})
}

func writeProviders(t *testing.T, dir, binName, words string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, binName+".providers"), []byte(words+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
