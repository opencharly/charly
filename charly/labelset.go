package main

// labelset.go — envelope for the three-section ai.opencharly.description
// OCI label payload carried by every opencharly image:
//
//   ai.opencharly.description → LabelDescriptionSet (BDD self-description)
//
// description is the three-section label carried by every image. A LabelSet
// aggregate wraps it so callers can carry the payload as a single value when
// convenient. The OCI label wire format is parsed by ParseLabels in labels.go.
//
// Section semantics of LabelDescriptionSet:
//   - Candy:  one entry per candy in the chain that contributes
//             a description.
//   - Box:    the box's own box-level entries.
//   - Deploy: deploy-scope entries (build-time defaults baked into
//             the image; charly.yml overlays merge into this section
//             at test/run time, not here).

// LabelSet is the Go-side envelope for an image's description label
// payload. Call sites use it to carry the description set as a single
// value (validators, MCP-style introspection); the BoxMetadata.Description
// field remains the canonical access point for code that reads the label
// directly.
type LabelSet struct {
	Descriptions *LabelDescriptionSet `json:"descriptions,omitempty"`
}

// IsEmpty returns true when the description payload carries no entries.
func (s *LabelSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return s.Descriptions.IsEmpty()
}

// LabelDescriptionSet is the three-section structure embedded in the
// ai.opencharly.description OCI label: candy-contributed descriptions
// (one per candy), box-level description (one), deploy-default
// description (one — usually from charly.yml overlays).
//
// Mirrors LabelShellSet's three-section shape so the collection + merge
// pipeline and the reporting format can share a mental model.
type LabelDescriptionSet struct {
	Candy  []LabeledDescription `json:"candy,omitempty"`
	Box    []LabeledDescription `json:"box,omitempty"`
	Deploy []LabeledDescription `json:"deploy,omitempty"`
}

// LabeledDescription is a plain-string description + its baked plan steps,
// with a collection-time origin annotation. Origin follows the
// `candy:<name>` / `box:<name>` / `deploy-default` / `deploy-local`
// convention also used by LabelShellSet entries' Origin field.
type LabeledDescription struct {
	Origin      string `json:"origin"`
	Description string `json:"description,omitempty"`
	Plan        []Step `json:"plan,omitempty"`
}

// IsEmpty returns true if no section has any descriptions. Used by label
// emission to omit the label entirely when there are none.
func (s *LabelDescriptionSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Candy) == 0 && len(s.Box) == 0 && len(s.Deploy) == 0
}
