package main

import "github.com/opencharly/sdk/vmshared"

// Test-only bindings onto the concrete authored substrate value types. Production charly
// consumes the de-typed Resolved* value envelopes (Cutovers I–L); ONLY tests construct the
// concrete spec shapes to seed fixtures and exercise the plugins. Keeping these in a
// _test.go file is what lets TestNoConcreteKindInKernel assert the production kernel holds
// no concrete-kind substrate type.
type (
	PodSpec     = vmshared.PodSpec
	K8sSpec     = vmshared.K8sSpec
	LocalSpec   = vmshared.LocalSpec
	AndroidSpec = vmshared.AndroidSpec
)
