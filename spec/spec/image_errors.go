package spec

import "errors"

// image_errors.go — image-storage sentinel errors (RELOCATED from sdk/kit box_metadata.go,
// #55 value extraction). A pure error sentinel over the image E-envelope, returned by the
// host metadata probe (sdk/kit.ExtractMetadata) and matched by charly core via errors.Is;
// homed in spec so charly matches spec.ErrImageNotLocal without importing sdk/kit. sdk/kit
// re-exports it so existing kit.ErrImageNotLocal call sites are untouched.

// ErrImageNotLocal is returned (wrapped with the image ref) when an image is not present in
// local container storage — a host-side signal, never crossing the plugin wire.
var ErrImageNotLocal = errors.New("image not found in local storage")

// ErrStaleLocalImage is returned (wrapped with both refs) when a short-name resolve elects a
// local image that is NOT the newest build of that box, for a verb that pronounces a verdict on
// the artifact. Certifying an artifact older than the one on disk produces a green result that
// proves nothing, so the resolution is refused rather than silently taken
// (container.ResolveBuiltImageRef).
var ErrStaleLocalImage = errors.New("short name resolves to a stale local image")
