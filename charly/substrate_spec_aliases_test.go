package main

import "github.com/opencharly/spec/spec"

// Test-only bindings onto the concrete authored substrate value types. Production charly
// consumes the de-typed Resolved* value envelopes (Cutovers I–L); ONLY tests construct the
// concrete spec shapes to seed fixtures and exercise the plugins. Keeping these in a
// _test.go file is what lets TestNoConcreteKindInKernel assert the production kernel holds
// no concrete-kind substrate type.
type (
	PodSpec     = spec.PodSpec
	K8sSpec     = spec.K8sSpec
	LocalSpec   = spec.LocalSpec
	AndroidSpec = spec.AndroidSpec
)
