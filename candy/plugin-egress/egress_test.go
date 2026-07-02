package egress

import "testing"

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
