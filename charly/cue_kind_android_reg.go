package main

// NB: filename intentionally does NOT end in `_android.go` — Go treats a
// `*_android.go` suffix as an implicit GOOS=android build constraint, which
// would silently exclude this registration from the linux build.

func init() { registerCueKind("android", "#Android") }
