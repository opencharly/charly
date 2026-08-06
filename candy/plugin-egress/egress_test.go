package egress

import (
	"os"
	"testing"
)

// TestEgressValidate is the M16 RDD spike: the egress validation logic + schemas, moved
// out of charly core into this plugin, still gate correctly — text mode, the vendored
// cloud_config separate-compile (the load-bearing risk), Concrete bytes mode, unknown kind.
func TestEgressValidate(t *testing.T) {
	p, err := newProvider()
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	cases := []struct {
		name        string
		in          validateInput
		wantInvalid bool
	}{
		{"text-good", validateInput{Kind: "rendered_text", Label: "cf", Mode: "text", Data: "FROM x\nRUN y\n"}, false},
		{"text-novalue", validateInput{Kind: "rendered_text", Label: "cf", Mode: "text", Data: "FROM x\nRUN <no value>\n"}, true},
		{"cloud_config-good", validateInput{Kind: "cloud_config", Label: "ud", Mode: "bytes", Data: "#cloud-config\nusers: []\n"}, false},
		{"deploy_record-good", validateInput{Kind: "deploy_record", Label: "rec", Mode: "bytes", Data: `{"deploy_id":"d1","target":"t1","deployed_at":"2026-06-30T00:00:00Z"}`}, false},
		{"deploy_record-missing-required", validateInput{Kind: "deploy_record", Label: "rec", Mode: "bytes", Data: `{}`}, true},
		{"unknown-kind", validateInput{Kind: "nope", Label: "x", Mode: "bytes", Data: "{}"}, true},
	}
	for _, c := range cases {
		got := p.validate(c.in)
		if c.wantInvalid && got == "" {
			t.Errorf("%s: expected a validation failure, got pass", c.name)
		}
		if !c.wantInvalid && got != "" {
			t.Errorf("%s: expected pass, got failure: %s", c.name, got)
		}
	}
}

// TestGoldenCloudInit_OutputValidatesAgainstSchema proves the real cloud-init renderer's real
// output satisfies the real egress gate — driven from a GOLDEN fixture (tools/golden-cloudinit,
// mirroring tools/golden-compile's precedent) rather than a live sdk/vmshared.RenderCloudInit
// call, so this file needs no sdk import. The golden fixture was captured by actually running
// RenderCloudInit with the SAME VmSpec/CloudInitRuntimeParams the former charly/egress_test.go
// used to construct live, with vmshared's OWN permissive ValidateEgress stub — so the checked-in
// bytes are exactly what the real renderer produces; THIS test is what proves those real bytes
// pass the REAL egress schema (a non-empty verdict would mean charly emits cloud-init that its
// own vendored schema rejects). Relocated here with the egress family (K-wave 2 cone R2): the
// shim that used to front this gate moved to candy/plugin-fleet, whose test binary cannot reach
// verb:egress — the schema is the load-bearing half, and it lives here.
func TestGoldenCloudInit_OutputValidatesAgainstSchema(t *testing.T) {
	p, err := newProvider()
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	userData, err := os.ReadFile("testdata/cloudinit_egress_golden_userdata.yaml")
	if err != nil {
		t.Fatalf("reading golden user-data fixture: %v", err)
	}
	if got := p.validate(validateInput{Kind: "cloud_config", Label: "golden cloud-init user-data", Mode: "bytes", Data: string(userData)}); got != "" {
		t.Fatalf("golden cloud-init user-data must pass the real egress gate, got: %v", got)
	}
	metaData, err := os.ReadFile("testdata/cloudinit_egress_golden_metadata.yaml")
	if err != nil {
		t.Fatalf("reading golden meta-data fixture: %v", err)
	}
	if got := p.validate(validateInput{Kind: "cloud_init_meta", Label: "golden cloud-init meta-data", Mode: "bytes", Data: string(metaData)}); got != "" {
		t.Fatalf("golden cloud-init meta-data must pass the real egress gate, got: %v", got)
	}
}
