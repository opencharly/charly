package spec

import "sort"

// builder_config_methods.go — pure Go METHODS on the CUE-generated BuilderConfig
// (schema/builder.cue -> spec/cue_types_gen.go). `cue exp gengotypes` has no
// construct for a method, so these stay hand-written here (see
// distro_config_methods.go). #55 step 3-III: ported verbatim from the former
// sdk/buildkit BuilderConfig (sdk/buildkit/format_config.go), now a thin alias.

// ValidBuilderType returns true if the given name is a defined builder.
func (bc *BuilderConfig) ValidBuilderType(name string) bool {
	if bc == nil {
		return false
	}
	_, ok := bc.Builder[name]
	return ok
}

// BuilderNames returns sorted list of defined builder names.
func (bc *BuilderConfig) BuilderNames() []string {
	if bc == nil {
		return nil
	}
	names := make([]string, 0, len(bc.Builder))
	for name := range bc.Builder {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
