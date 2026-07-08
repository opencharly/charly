package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestLibvirtListeners_OpaqueNotExpander locks the P1.4 fix: LibvirtGraphicsListeners
// is a json.Unmarshaler (so the CUE normalizer short-circuits it and cue.Value.Decode
// runs its tri-shape UnmarshalJSON on BOTH the YAML and the opaque substrate-decode
// path), and it is therefore NOT registered in cueShorthandExpanders — a re-added
// expander entry (or a lost UnmarshalJSON) would reintroduce the check-cross-vm-http
// regression.
func TestLibvirtListeners_OpaqueNotExpander(t *testing.T) {
	if !implementsJSONUnmarshaler(reflect.TypeOf(LibvirtGraphicsListeners{})) {
		t.Fatal("LibvirtGraphicsListeners must implement json.Unmarshaler (the opaque-decode path)")
	}
	if _, ok := cueShorthandExpanders[reflect.TypeOf(LibvirtGraphicsListeners{})]; ok {
		t.Fatal("LibvirtGraphicsListeners must NOT be in cueShorthandExpanders — its UnmarshalJSON serves both read paths")
	}
	// The scalar shorthand the vm web-vm bed uses decodes on the JSON (opaque) path.
	var ll LibvirtGraphicsListeners
	if err := json.Unmarshal([]byte(`"127.0.0.1"`), &ll); err != nil {
		t.Fatalf("scalar listen shorthand must decode on the opaque path: %v", err)
	}
	if len(ll) != 1 || ll[0].Type != "address" || ll[0].Address != "127.0.0.1" {
		t.Fatalf("unexpected decode: %+v", ll)
	}
}
