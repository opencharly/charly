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
