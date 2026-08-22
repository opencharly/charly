package spec

import (
	"encoding/json"
	"testing"
)

// TestLibvirtListeners_JSONTriShape proves the JSON read path (the opaque
// substrate-decode path) accepts the same three shorthand shapes the YAML path
// always did — the check-cross-vm-http regression from the vm-body de-typing.
func TestLibvirtListeners_JSONTriShape(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want LibvirtGraphicsListeners
	}{
		{"scalar-address", `"127.0.0.1"`, LibvirtGraphicsListeners{{Type: "address", Address: "127.0.0.1"}}},
		{"single-object-typed", `{"type":"socket"}`, LibvirtGraphicsListeners{{Type: "socket"}}},
		{"single-object-infer-address", `{"address":"0.0.0.0"}`, LibvirtGraphicsListeners{{Type: "address", Address: "0.0.0.0"}}},
		{"single-object-infer-network", `{"network":"default"}`, LibvirtGraphicsListeners{{Type: "network", Network: "default"}}},
		{"list", `[{"type":"socket"},{"address":"1.2.3.4"}]`, LibvirtGraphicsListeners{{Type: "socket"}, {Type: "address", Address: "1.2.3.4"}}},
		{"null", `null`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got LibvirtGraphicsListeners
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d want %d (%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
	// A malformed shape (bare number) is a loud error, not a silent zero.
	var bad LibvirtGraphicsListeners
	if err := json.Unmarshal([]byte(`42`), &bad); err == nil {
		t.Error("bare number must fail")
	}
}
