package pod

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// service_resolve_test.go — resolveInitDefFromMeta/initRenderManagementCommand coverage relocated
// from charly/init_def_label_test.go (Cutover B unit 2 service-verb completion) alongside the
// functions themselves.

// TestResolveInitDefFromMeta_LabelRequired proves the management surface comes ONLY from the
// baked ai.opencharly.init_def label. The frozen wellKnownInitDefs fallback table it used to
// consult is deleted: it was legacy support for images built before the label existed, it was
// triplicated across sdk/kit, this plugin and plugin-deploy-pod, and it was frozen at
// supervisord + systemd — so with openrc now a first-class init system, the table could only
// ever answer for two of the three, silently.
//
// An image without the label errors, naming the rebuild. That is the point: a management
// command guessed for an unknown init system runs the WRONG supervisor's CLI.
func TestResolveInitDefFromMeta_LabelRequired(t *testing.T) {
	for _, init := range []string{"supervisord", "systemd", "openrc", "vocab-only-custom"} {
		if _, err := resolveInitDefFromMeta(&spec.BoxMetadata{Init: init}); err == nil {
			t.Errorf("init %q with no init_def label must error — there is no fallback table", init)
		}
	}
}

// TestInitDefLabel_CustomInitAtRuntime proves the capability that makes the deleted table
// unnecessary: an init system declared ONLY in the embedded vocabulary resolves at RUNTIME from
// the baked label, with nothing to register in Go. This is the mechanism openrc: uses.
func TestInitDefLabel_CustomInitAtRuntime(t *testing.T) {
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
