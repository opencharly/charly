package main

// checkenv_wire_test.go — the generic wire/round-trip guard carried out of the
// retired committed-APK wire test. The mechanism under test — snapshotCheckEnv
// re-emitting the carrier's CandyDirs so a nested out-of-process verb dispatch
// keeps its candy source-dir map — is KEPT (charly/provider_checkenv.go,
// charly/plugin_dispatch_reverse.go); only the committed-APK anchor that once
// motivated it (resolveCheckApk) retired with the Android substrate.

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestSnapshotCheckEnvCarriesCandyDirs — the wire env a nested out-of-process
// verb dispatch carries re-emits the carrier's CandyDirs, so a second hop (host
// → another out-of-process provider) keeps the candy source-dir map.
func TestSnapshotCheckEnvCarriesCandyDirs(t *testing.T) {
	cc := &hostCheckCarrier{
		mode: spec.CheckModeLive,
		box:  "check-pod",
		candyDirs: map[string]string{
			"pod-testapi": "/srv/charly/candy/pod-testapi",
		},
	}
	ce := snapshotCheckEnv(cc, nil)
	if len(ce.CandyDirs) != 1 || ce.CandyDirs["pod-testapi"] != "/srv/charly/candy/pod-testapi" {
		t.Fatalf("snapshotCheckEnv lost the candy_dirs map: got %+v", ce.CandyDirs)
	}
	// Round-trips through the wire JSON (the field's json tag is what crosses).
	wire, err := json.Marshal(ce)
	if err != nil {
		t.Fatal(err)
	}
	var back spec.CheckEnv
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.CandyDirs) != 1 || back.CandyDirs["pod-testapi"] != "/srv/charly/candy/pod-testapi" {
		t.Errorf("candy_dirs did not survive the wire round-trip: %+v", back.CandyDirs)
	}
}
