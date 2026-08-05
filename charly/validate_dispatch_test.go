package main

// validate_dispatch_test.go — the TEST-ONLY reproduction of the pre-build validation GATE.
//
// The gate itself is not core code any more. `charly box build` / `charly box generate` run their
// pre-build validate PLUGIN-SIDE, as a genuine plugin↔plugin dispatch: candy/plugin-build's
// validateProjectLeg calls InvokeProvider(command:validate, ops.OpValidate) and reads back
// spec.Diagnostics. Core's own copy of that dispatch (validate_project_host.go's
// validateProjectForBuild) lost its last production caller when #55 step3 3-II deleted the host-side
// NewGenerator, and survived only because the fixture tests below drove it; K-wave 2 cone R1 unit B
// moved it here, so the production file carries none of it.
//
// This helper IS candy/plugin-build's validateProjectLeg, expressed against the same compiled-in
// registry the plugin reaches over its in-proc reverse channel — so the fixture tests keep exercising
// the real dispatch (parse → load → resolve → plugin rules → diagnostics → verdict text) rather than
// a stub, and the error text they assert is the one a user sees.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// dispatchValidateForTest resolves the compiled-in command:validate capability, invokes it with a
// structured ops.OpValidate op, and renders the returned spec.Diagnostics as the SAME error text
// spec.ValidationError.Error() produced — "validation error: <m>" for one finding, "N validation
// errors:\n\n  <joined>" for several. Warning-severity items never reach the verdict.
func dispatchValidateForTest(dir string, opts spec.ResolveOpts) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "validate")
	if !ok {
		return fmt.Errorf("pre-build validation: the validate capability (command:validate) is not compiled in")
	}
	reqJSON, err := json.Marshal(spec.ValidateProjectRequest{Dir: dir, IncludeDisabled: opts.IncludeDisabled})
	if err != nil {
		return err
	}
	res, err := prov.Invoke(hostInProcCtx(), &Operation{Reserved: "validate", Op: ops.OpValidate, Params: reqJSON})
	if err != nil {
		return err
	}
	var diags spec.Diagnostics
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &diags); uerr != nil {
			return fmt.Errorf("pre-build validation: decode diagnostics: %w", uerr)
		}
	}
	msgs := make([]string, 0, len(diags.Items))
	for _, it := range diags.Items {
		if it.Severity == "warning" {
			continue
		}
		msgs = append(msgs, it.Message)
	}
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) == 1 {
		return fmt.Errorf("validation error: %s", msgs[0])
	}
	return fmt.Errorf("%d validation errors:\n\n  %s", len(msgs), strings.Join(msgs, "\n  "))
}
