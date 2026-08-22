// CUE schema for the `resource` kind. #Resource validates ONE value of the
// `resource:` map (ResourceDef — exclusive host-resource tokens for GPU
// arbitration). CLOSED. Both shapes valid: `{}` (bare arbitration token) and
// `{gpu: {vendor: ...}}` (selector token). No #Step.

#Resource: {
	gpu?: #GpuSelector @go(Gpu,optional=nillable)
}

// vendor required + non-empty; NOT regex-pinned (normalizePCIVendor accepts
// "10DE"/"0X10de"/"0x10de" — a strict regex would reject Go-valid input).
#GpuSelector: {
	vendor: string & !=""
}

// --- resolve-to-envelope wire types (Cutover G; SDD conversion, per the standing
// operator directive: a hand-written wire struct not yet CUE-sourced is
// conversion-in-progress, never a sanctioned exception). candy/plugin-resource
// resolves an authored `resource:` entity into a ResolvedResource the kernel's
// GPU arbiter consumes without importing the concrete spec.Resource. Plain
// structs — gengotypes generates them faithfully, no disjunction needed.

#ResolvedGpuSelector: {
	vendor?: string
}

#ResolvedResource: {
	gpu?: #ResolvedGpuSelector @go(Gpu,optional=nillable)
}

#ResourceResolveInput: {
	resource!: bytes @go(Resource,type=RawBody)
}

#ResourceResolveReply: {
	resolved?: #ResolvedResource @go(Resolved,optional=nillable)
}
