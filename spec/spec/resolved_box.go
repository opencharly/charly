package spec

import "slices"

// resolved_box.go — the hand-written METHODS for the CUE-generated #ResolvedBox
// (schema/resolvedbox.cue → cue_types_gen.go). gengotypes has no construct for behavior, so
// SupportsTag/SupportsBuild (and BuilderMap's BuilderFor/HasBuilder/AllBuilder, below) stay
// hand-written here alongside the generated types (the Op.Kind()-style exception; #55 step3
// unit 3a).

// SupportsTag returns true if this image has the given tag.
// Tags include format (rpm, deb, pac), distro (fedora, arch),
// version (fedora:43), and the implicit "all".
func (img *ResolvedBox) SupportsTag(tag string) bool {
	return slices.Contains(img.Tags, tag)
}

// SupportsBuild returns true if this image has the given build format.
func (img *ResolvedBox) SupportsBuild(format string) bool {
	return slices.Contains(img.BuildFormats, format)
}

// BuilderFor returns the builder image name for the given format, or "".
func (m BuilderMap) BuilderFor(format string) string {
	return m[format]
}

// HasBuilder returns true if a builder is configured for the given format.
func (m BuilderMap) HasBuilder(format string) bool {
	return m[format] != ""
}

// AllBuilder returns a deduplicated sorted list of builder image names.
func (m BuilderMap) AllBuilder() []string {
	seen := make(map[string]bool)
	var builders []string
	for _, b := range m {
		if b != "" && !seen[b] {
			seen[b] = true
			builders = append(builders, b)
		}
	}
	slices.Sort(builders)
	return builders
}
