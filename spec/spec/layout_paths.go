package spec

// layout_paths.go — the stable per-directory discovery path constants (the
// discovered box/<name>/ and candy/<name>/ dirs). Relocated from
// sdk/kit/migrate_support.go (#55 import-purity cone-render): plain loader-result
// DATA the types-only spec module owns, mirroring spec.UnifiedFileName. kit
// re-exports them via const alias so existing kit.DefaultBoxDir / kit.DefaultCandyDir
// call sites (plugins + sdk) are untouched.
const (
	DefaultBoxDir   = "box"   // discovered box/<name>/ directory
	DefaultCandyDir = "candy" // discovered candy/<name>/ directory
)
