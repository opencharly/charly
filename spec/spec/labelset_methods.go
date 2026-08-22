package spec

// labelset_methods.go — hand methods on the generated #LabelDescriptionSet (boxmetadata.cue,
// P2B). The type is CUE-sourced (kit.LabelDescriptionSet aliases it); its behaviour lives here
// beside the type, per the charly_methods.go / tunnel_methods.go precedent.

// IsEmpty reports whether the set carries no labeled descriptions in any layer.
func (s *LabelDescriptionSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Candy) == 0 && len(s.Box) == 0 && len(s.Deploy) == 0
}
