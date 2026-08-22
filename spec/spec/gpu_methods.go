package spec

// gpu_methods.go — a pure Go CONSTRUCTOR for the CUE-generated, FLATTENED
// VFIOGpu (sdk/schema/gpu.cue -> spec/cue_types_gen.go). VFIOGpu used to embed
// VFIOPCIDevice anonymously (Go's own JSON-promotion rule already made the
// wire shape identical either way); CUE unification has no concept of Go
// embedding, so the SDD conversion flattened VFIOGpu's fields directly.
// NewVFIOGpu restores the one-line "build a VFIOGpu from a VFIOPCIDevice"
// ergonomic the former embedded-field composite literal
// (`VFIOGpu{VFIOPCIDevice: d}`) provided, so every construction site (charly,
// candy/plugin-gpu) names ONE spread point instead of repeating the 7-field
// copy (R3) — set GroupMembers on the returned value afterward, exactly as
// the former embedded-literal call sites already did.
func NewVFIOGpu(d VFIOPCIDevice) VFIOGpu {
	return VFIOGpu{
		Addr:       d.Addr,
		VendorID:   d.VendorID,
		DeviceID:   d.DeviceID,
		Class:      d.Class,
		ClassLabel: d.ClassLabel,
		Driver:     d.Driver,
		IOMMUGroup: d.IOMMUGroup,
	}
}
