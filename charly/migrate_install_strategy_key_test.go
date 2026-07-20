package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// The rename target MUST equal the live struct's yaml tag — otherwise the
// migrator would rewrite the key to a name the loader still drops. This pins the
// two together so a future tag rename can't silently desync the migrator.
func TestInstallStrategyKey_MatchesStructTag(t *testing.T) {
	f, ok := reflect.TypeOf(spec.VmDeployState{}).FieldByName("CharlyInstallStrategy")
	if !ok {
		t.Fatal("VmDeployState has no CharlyInstallStrategy field")
	}
	tag := f.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name != "charly_install_strategy" {
		t.Fatalf("VmDeployState.CharlyInstallStrategy yaml tag = %q, want charly_install_strategy", name)
	}
}
