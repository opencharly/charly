package main

// migrate_testsupport_test.go — test-only shims. The HEAD schema version lives in
// charly/plugin/kit (CUE-owned via spec.SchemaVersion); several core tests reference
// the bare `latestSchemaVersion` identifier, so this test-only alias keeps them
// compiling against the ONE kit copy (production code uses the LatestSchemaVersion() shim).
var latestSchemaVersion = LatestSchemaVersion()
