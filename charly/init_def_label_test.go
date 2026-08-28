package main

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"github.com/opencharly/spec/spec"
)

// TestInitDefLabel_EmbeddedVocabNonSparse proves the embedded build vocabulary's
// supervisord init def carries non-trivial values — a sanity guard against a
// vocab edit accidentally emptying Entrypoint/ManagementTool/ManagementCommands
// (K3 cone2 test closure narrowed this file to charly's OWN embedded-config
// concern; the bake(WriteLabels)→parse(ExtractMetadata) round-trip proof this
// file used to also carry — a pure sdk/deploykit + spec/container concern with
// no charly dependency — moved to sdk/deploykit/init_def_label_test.go with a
// literal *spec.CapabilityInitDef fixture in place of this vocab). Calls
// spec.ProjectInitConfig directly (the canonical home, matching loader_threaded.go's
// own production call) rather than the sdk/loaderkit re-export the original test
// used — dropping the sdk/loaderkit import charly core doesn't otherwise need.
func TestInitDefLabel_EmbeddedVocabNonSparse(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil || ic.Init["supervisord"] == nil {
		t.Fatal("embedded vocabulary missing supervisord init def")
	}
	def := ic.Init["supervisord"]

	if len(def.Entrypoint) == 0 || def.ManagementTool == "" || len(def.ManagementCommands) == 0 {
		t.Fatalf("embedded supervisord vocab unexpectedly sparse: %+v", def)
	}
}

// TestEmbeddedVocab_OpenRCIsAFirstClassInit proves openrc: is a peer of systemd: and
// supervisord: in the embedded vocabulary, and that its SHIPPED service template actually
// renders a valid OpenRC script.
//
// Field presence alone is not proof: the existing render tests in candy/plugin-init use LOCAL
// template constants, so nothing exercised the templates this binary actually ships. A
// template that fails to parse, or renders a script OpenRC would refuse, would otherwise
// surface only in a bed — and OpenRC silently ignores a malformed or non-executable
// /etc/init.d file, which presents as "service does not exist" rather than a parse error.
func TestEmbeddedVocab_OpenRCIsAFirstClassInit(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil {
		t.Fatal("embedded vocabulary has no init config")
	}
	def := ic.Init["openrc"]
	if def == nil {
		t.Fatal("embedded vocabulary missing the openrc init def — OpenRC would exist only as a " +
			"hardcoded branch again")
	}

	// Peer parity: whatever the two established init systems declare, openrc declares too.
	// This is the assertion that "first-class" means something checkable.
	if def.ManagementTool == "" || len(def.ManagementCommands) == 0 {
		t.Errorf("openrc init def is sparse: tool=%q commands=%v", def.ManagementTool, def.ManagementCommands)
	}
	for _, op := range []string{"start", "stop", "restart", "status"} {
		if def.ManagementCommands[op] == "" {
			t.Errorf("openrc declares no %q management command; systemd and supervisord both do", op)
		}
	}
	if def.ServiceSchema == nil || def.ServiceSchema.ServiceTemplate == "" {
		t.Fatal("openrc declares no service_schema.service_template — a candy's service: entry " +
			"could not be rendered into an /etc/init.d script")
	}
	if def.ServiceSchema.UnitPathTemplate == "" {
		t.Error("openrc declares no unit_path_template — the script would have nowhere to land")
	}

	// The shipped template must parse and render. Context mirrors what the init renderer
	// supplies for a service.
	//
	// openrcLog is STUBBED, not reimplemented: this test asserts the template parses and
	// renders, and the real mapping (journal -> omitted, none -> /dev/null, file:<p> -> <p>)
	// is owned by candy/plugin-init's serviceRenderFuncs and pinned by its TestOpenrcLogging.
	// Duplicating the semantics here would create a second source of truth that could drift
	// from the one the renderer actually uses.
	tmpl, err := template.New("openrc-service").
		Funcs(template.FuncMap{"openrcLog": func(string) string { return "" }}).
		Parse(def.ServiceSchema.ServiceTemplate)
	if err != nil {
		t.Fatalf("openrc service_template does not parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"Name": "demo", "Candy": "demo-candy", "Exec": "/usr/bin/demo --serve",
		"Restart": "always", "User": "demo", "WorkingDirectory": "/srv/demo",
		"StopTimeout": "30", "After": []string{"net"}, "Before": []string{},
		"EnvList": []map[string]string{{"Key": "MODE", "Value": "live"}},
		// The template reads .Stdout; an absent key renders as an invalid reflect.Value.
		"Stdout": "",
	}); err != nil {
		t.Fatalf("openrc service_template failed to render: %v", err)
	}
	got := buf.String()

	// Assert the properties OpenRC itself requires, not the exact bytes — a golden string
	// here would have to be re-baselined by every legitimate template edit.
	for _, want := range []string{
		"#!/sbin/openrc-run",    // OpenRC refuses a script without its interpreter line
		"supervise-daemon",      // the restart semantics systemd expresses with Restart=
		"depend()",              // dependency ordering must be declared in a depend function
		"/usr/bin/demo --serve", // the authored exec reaches the script
		"MODE=",                 // env is exported
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered openrc script is missing %q:\n%s", want, got)
		}
	}
	// It must NOT emit systemd syntax — the failure mode if the template were copied.
	for _, unwanted := range []string{"[Unit]", "[Service]", "ExecStart=", "systemctl"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("rendered openrc script contains systemd syntax %q:\n%s", unwanted, got)
		}
	}
}
