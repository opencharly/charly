package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// The RenderService template-render matrix relocated to
// candy/plugin-init/render_service_unit_test.go (#55 W3 B4): RenderService/
// renderServiceViaPlugin are deleted — charly core no longer holds a front door to
// candy/plugin-init's render logic at all (sdk/deploykit's renderSeamCaller.renderService
// reaches it via direct InvokeProvider peer dispatch instead). The two tests below are
// unrelated to that seam (spec.ServiceEntry's own pure methods) and stay here.

func TestServiceEntryIsPackaged(t *testing.T) {
	packaged := &spec.ServiceEntry{UsePackaged: "foo.service"}
	custom := &spec.ServiceEntry{Exec: "/bin/foo"}
	if !packaged.IsPackaged() {
		t.Errorf("packaged entry should return IsPackaged=true")
	}
	if custom.IsPackaged() {
		t.Errorf("custom entry should return IsPackaged=false")
	}
	var nilEntry *spec.ServiceEntry
	if nilEntry.IsPackaged() {
		t.Errorf("nil entry should return IsPackaged=false")
	}
}

func TestServiceEntryEffectiveScope(t *testing.T) {
	if got := (&spec.ServiceEntry{}).EffectiveScope(); got != "system" {
		t.Errorf("default scope = %q, want system", got)
	}
	if got := (&spec.ServiceEntry{Scope: "user"}).EffectiveScope(); got != "user" {
		t.Errorf("explicit user scope = %q", got)
	}
}
