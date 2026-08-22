package spec

import "strings"

// yaml_version_line.go — the first top-level `version:` line extractor, RELOCATED from
// sdk/kit/migrate_helpers.go (#55 coneG import-purity: charly core's refs.go inlines
// spec.FirstYAMLVersionLine, dropping its sdk/kit import). Pure string parsing —
// strings.SplitSeq + strings.CutPrefix + strings.TrimSpace, NO yaml.v3 / NO subprocess —
// so it lives in the universal spec/spec slice (std-lib only). sdk/kit re-exports it
// (kit/migrate_helpers.go: var FirstYAMLVersionLine = spec.FirstYAMLVersionLine) so the
// existing kit.FirstYAMLVersionLine plugin call sites (candy/plugin-migrate/engine.go)
// are untouched; new charly-core consumers reference spec.* directly.

// FirstYAMLVersionLine extracts the value of the first top-level `version:` line.
func FirstYAMLVersionLine(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}
