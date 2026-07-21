package pod

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// service_resolve_test.go — resolveInitDefFromMeta/initRenderManagementCommand coverage relocated
// from charly/init_def_label_test.go (Cutover B unit 2 service-verb completion) alongside the
// functions themselves.

// TestResolveInitDefFromMeta_LegacyLabelAbsent proves the management-surface
// fallback still resolves supervisord + systemd from wellKnownInitDefs when
// the init_def label is absent, and errors on a truly unknown legacy init.
func TestResolveInitDefFromMeta_LegacyLabelAbsent(t *testing.T) {
	for _, tc := range []struct{ init, tool string }{
		{"supervisord", "supervisorctl"},
		{"systemd", "systemctl"},
	} {
		meta := &spec.BoxMetadata{Init: tc.init} // InitDef nil → legacy fallback
		def, err := resolveInitDefFromMeta(meta)
		if err != nil {
			t.Fatalf("resolveInitDefFromMeta(%q): %v", tc.init, err)
		}
		if def.ManagementTool != tc.tool {
			t.Errorf("init %q: management tool = %q, want %q", tc.init, def.ManagementTool, tc.tool)
		}
	}

	if _, err := resolveInitDefFromMeta(&spec.BoxMetadata{Init: "vocab-only-custom"}); err == nil {
		t.Error("resolveInitDefFromMeta with unknown init + no label should error")
	}
}

// TestInitDefLabel_CustomInitAtRuntime proves the capability win: an init
// system declared ONLY in the vocabulary (so absent from wellKnownInitDefs)
// now resolves at RUNTIME via the baked label — the prior build-only
// limitation is gone. The management surface comes from meta.InitDef even
// though "myinit" has no registry entry.
func TestInitDefLabel_CustomInitAtRuntime(t *testing.T) {
	if _, ok := wellKnownInitDefs["myinit"]; ok {
		t.Fatal("precondition: myinit must NOT be a well-known init")
	}
	meta := &spec.BoxMetadata{
		Init: "myinit",
		InitDef: &spec.CapabilityInitDef{
			Entrypoint:         []string{"myinit", "--run", "/etc/myinit.conf"},
			ManagementTool:     "myctl",
			ManagementCommands: map[string]string{"status": "status", "restart": "restart {{.Service}}"},
		},
	}

	gotDef, err := resolveInitDefFromMeta(meta)
	if err != nil {
		t.Fatalf("resolveInitDefFromMeta(custom): %v", err)
	}
	if gotDef.ManagementTool != "myctl" {
		t.Errorf("custom init management tool = %q, want myctl", gotDef.ManagementTool)
	}

	// Render a management command end-to-end to prove the baked commands are usable.
	rendered, err := initRenderManagementCommand(gotDef, "restart", "web")
	if err != nil {
		t.Fatalf("initRenderManagementCommand: %v", err)
	}
	if rendered != "restart web" {
		t.Errorf("rendered restart command = %q, want %q", rendered, "restart web")
	}
}
