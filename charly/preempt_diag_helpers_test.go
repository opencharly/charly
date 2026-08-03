package main

import (
	"strings"

	"github.com/opencharly/spec/spec"
)

// preemptDiagHasErr / preemptDiagText are the spec.ValidationError.HasErrors /
// .Error analogues over the spec.Diagnostics loaderkit.ValidatePreemptibleOnNode
// accumulates into. Kept here (split out of the now-relocated
// validate_preempt_test.go — #55 decoupling cone, Batch C) SOLELY because
// preempt_schema_test.go's own TestValidatePreemptibleOnNode (owned by Batch A,
// per the binding file-ownership matrix — that file is untouched by this
// batch) still calls them; Batch A's own relocation of that test removes this
// file's need to exist.
func preemptDiagHasErr(d spec.Diagnostics) bool {
	for _, it := range d.Items {
		if it.Severity == "error" {
			return true
		}
	}
	return false
}

func preemptDiagText(d spec.Diagnostics) string {
	var msgs []string
	for _, it := range d.Items {
		if it.Severity == "error" {
			msgs = append(msgs, it.Message)
		}
	}
	return strings.Join(msgs, "\n")
}
