package main

import (
	"github.com/opencharly/sdk/spec"
)

// OCI label key constants (all namespaced under ai.opencharly.) live in sdk/spec
// (label_consts.go — the build↔deploy wire contract: the deploykit WriteLabels EMITTER
// + the deploykit.ExtractMetadata deploy READER share one copy). Every charly/ reader
// references spec.LabelX directly (K3 ZERO-ALIASES dissolution — an alias is always
// residue regardless of what it aliases).

// BoxMetadata + the OCI-label sub-shapes are CUE-sourced in spec (boxmetadata.cue, P2B, #60)
// and aliased IN-PLACE here (spec is the allowed import; these are NOT collected into an
// *_aliases.go file, per the ZERO-ALIASES gate). Field docs + the exact JSON wire tags live
// on the CUE defs. InspectLabels + ExtractMetadata (the label decode logic itself) moved to
// sdk/deploykit/read_labels.go (K3-rem, shared-kit extraction) — pure functions of
// (engine, imageRef), used by build/deploy/check alike; charly callers reference
// deploykit.ExtractMetadata / deploykit.InspectLabels directly.
type (
	LabelVolumeEntry  = spec.LabelVolumeEntry
	LabelRouteEntry   = spec.LabelRouteEntry
	CapabilityService = spec.CapabilityService
	CapabilityInitDef = spec.CapabilityInitDef
	LabelDataEntry    = spec.LabelDataEntry
	BoxMetadata       = spec.BoxMetadata
	LabelShellSet     = spec.LabelShellSet
	ShellEntry        = spec.ShellEntry
)
