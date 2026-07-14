package main

// validate_check_test.go — the former validateOps host tests (multi-verb, runtime-var-in-build-context,
// the mcp/record/spice/libvirt "clean" fixtures, and the kube lowercase-${} guard) moved with the
// Op-level plan validator to candy/plugin-box (task #60). They are re-expressed as on-disk fixtures
// driven through the real `charly box validate` gate in validate_fixture_test.go (TestValidateOps_*),
// and the mcp/record/spice/libvirt method-enum rejections are CUE-enforced (cue_tighten_test.go). The
// former `opsCandy` / `runValidateOps` synthetic helpers were deleted with the host validateOps.
