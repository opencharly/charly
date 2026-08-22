package spec

// injection_seams.go — process-wide dependency-injection seam VARS the kernel
// (charly) fills at init and the fabric libraries read. Relocated here (#55
// import-purity cone-render) so the injector (charly) and the consumers (the sdk
// libraries) share ONE canonical var without a kit/deploykit import.

// ValidateRecord is the egress-validation seam for install-ledger writes. The ledger
// validates each record against its egress schema before writing; charly injects its
// ValidateEgressValue here at init. Defaults to a no-op for standalone use.
var ValidateRecord = func(kind, label string, v any) error { return nil }

// OpInContext reports whether an op runs in the given exec context. Its fallback
// consults the kernel VerbCatalog (charly), so charly injects the impl at init.
var OpInContext func(op *Op, ctx ExecContext) bool
