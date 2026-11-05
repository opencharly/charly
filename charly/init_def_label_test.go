package main

import (
	"bytes"
	"regexp"
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
		Funcs(template.FuncMap{
			"openrcLog": func(string) string { return "" },
			// Same reasoning as openrcLog: the flattening semantics belong to
			// candy/plugin-init's initDirectives and are pinned by its
			// TestInitDirectivesFlattensUnitOptions. Returning an empty slice keeps
			// this test about "the template parses and renders".
			"initDirectives": func(map[string]map[string]any, string) []struct{ Key, Value string } {
				return nil
			},
			// Third stub, same reasoning: the lowering is owned by
			// candy/plugin-init's waitForScript and pinned by its TestWaitForScript.
			"waitForScript": func(any) string { return "" },
		}).
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
		// Same for the B2 fields the template now references.
		"Requires": []string{}, "RestartSec": "", "WatchdogSec": "",
		"UnitOptions": map[string]map[string]any{},
		"WaitFor":     nil,
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

// TestEmbeddedVocab_SystemdClaimsEveryUnitTypeItInstalls guards the pair that has to
// stay consistent: the unit types systemd's candy_file: CLAIMS, and the types its
// assembly_template actually COPIES and ENABLES.
//
// They were inconsistent by construction before. candy_file: listed only *.service
// while the assembly hardcoded that same extension twice, so the pair agreed only
// because both were frozen — a candy could not ship a .socket or .target at all, and
// widening one side without the other would stage units the image never copies (or
// enable a .slice, which is an error rather than a no-op).
func TestEmbeddedVocab_SystemdClaimsEveryUnitTypeItInstalls(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil {
		t.Fatal("embedded vocabulary has no init config")
	}
	// The systemd FAMILY shares this claim/assembly pair: the machine init and the
	// container variant author their assemblies separately (extends: shares only
	// service_schema), so a widened candy_file: with a forgotten assembly edit is a
	// real hazard in either entry.
	for _, name := range []string{"systemd", "systemd-container"} {
		t.Run(name, func(t *testing.T) {
			def := ic.Init[name]
			if def == nil {
				t.Fatalf("embedded vocabulary missing %s init def", name)
			}

			claimed := map[string]bool{}
			for _, p := range def.CandyFiles {
				claimed[strings.TrimPrefix(p, "*")] = true
			}
			// The service unit is the historical floor; the rest are what the widening added.
			for _, ext := range []string{".service", ".socket", ".target"} {
				if !claimed[ext] {
					t.Errorf("%s candy_file: does not claim %s — a candy shipping one is "+
						"silently ignored, because the glob happens at the point of use", name, ext)
				}
			}

			asm := def.AssemblyTemplate
			if asm == "" {
				t.Fatalf("%s declares no assembly_template", name)
			}
			// The staged-unit DIRECTORY is read from the assembly's own mount TARGET, not
			// assumed to be /systemd: the target is an implementation detail that already
			// had to move (BuildKit does not materialize a top-level /system* target —
			// measured), and a test that pins the literal path breaks on a correct rename
			// while proving nothing about the semantics.
			m := regexp.MustCompile(`target=(/[^,\s]+)`).FindStringSubmatch(asm)
			if m == nil {
				t.Fatalf("%s assembly declares no mount target directory: %s", name, asm)
			}
			unitDir := m[1]
			// The target must NOT sit in the top-level /system* namespace: BuildKit
			// does not materialize such a mount target, so the bind mounts nothing and
			// the copy silently installs no units. This is the assertion that fails if
			// either assembly's target is reverted to /systemd — the machine init's fix
			// is a config line with no bed of its own in this repo, so this test is the
			// gate that catches its reversion.
			if strings.HasPrefix(unitDir, "/system") {
				t.Errorf("%s mounts its units at %q, inside the top-level /system* namespace "+
					"BuildKit does not materialize, so the assembly would copy no units", name, unitDir)
			}
			// The copy must not be extension-specific: a claimed type the copy misses is
			// staged into the build context and then dropped on the floor.
			if strings.Contains(asm, "cp "+unitDir+"/*.service ") {
				t.Error("the assembly still copies only *.service, so every other claimed unit " +
					"type is staged and then never installed")
			}
			// Enabling must cover the enableable claimed types...
			for _, ext := range []string{".service", ".socket", ".target"} {
				if !strings.Contains(asm, unitDir+"/*"+ext) {
					t.Errorf("the assembly never enables %s units, though candy_file: claims them", ext)
				}
			}
			// ...and must NOT enable a slice, which systemctl rejects.
			if strings.Contains(asm, "enable") && strings.Contains(asm, "*.slice") {
				t.Error("the assembly enables *.slice; a slice is a resource container referenced " +
					"by other units' Slice=, and enabling one is an error")
			}
			// An unmatched glob stays literal in sh, so a candy shipping only services would
			// otherwise fail the build on `systemctl enable '/systemd/*.socket'`.
			if !strings.Contains(asm, `[ -e "$unit" ] || continue`) {
				t.Error("the enable loop has no existence guard: an unmatched glob stays literal " +
					"in sh and would fail the build for a candy that ships only one unit type")
			}
		})
	}
}

// TestEmbeddedVocab_SystemdContainerExtends pins the ONE production use of the
// init vocabulary's `extends:` mechanism. The merge rule is zero-value
// inheritance per FIELD of ResolvedInit (spec/spec/init_extends.go): a child's
// non-empty field wins, an empty one inherits. Two consequences are asserted
// here because both are silent when wrong:
//
//   - requires_capability is non-empty in the child, so it WINS. The container
//     init must NOT inherit systemd:'s [preserve_user] — inheriting it would
//     both gate the entry on an unrelated capability and let a non-root USER
//     survive, which systemd refuses at PID 1 (exit 255 / start-limit-hit).
//   - service_schema is inherited as ONE field, service_template included. A
//     child that declares any part of service_schema wins the WHOLE field
//     instead, and the emitted init def then carries an EMPTY service_template —
//     every custom service entry fails to render against it.
func TestEmbeddedVocab_SystemdContainerExtends(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil || ic.Init["systemd"] == nil || ic.Init["systemd-container"] == nil {
		t.Fatal("embedded vocabulary missing systemd or systemd-container init def")
	}
	base := ic.Init["systemd"]
	def := ic.Init["systemd-container"]

	if def.Extends != "" {
		t.Errorf("systemd-container still carries extends=%q; the merge did not run", def.Extends)
	}

	// The child's own non-empty slice WINS.
	for _, capability := range def.RequiresCapability {
		if capability == "preserve_user" {
			t.Errorf("systemd-container inherited requires_capability %q from systemd; a "+
				"non-empty child slice must win, or the container init is gated on the wrong "+
				"capability and the image keeps a non-root USER systemd refuses at PID 1",
				capability)
		}
	}
	if len(def.RequiresCapability) != 1 || def.RequiresCapability[0] != "init_system:systemd-container" {
		t.Errorf("systemd-container requires_capability = %v, want [init_system:systemd-container]",
			def.RequiresCapability)
	}

	// The template the child leaves empty IS inherited from the entry it names.
	if base.ServiceSchema == nil || base.ServiceSchema.ServiceTemplate == "" {
		t.Fatal("systemd declares no service_template to inherit")
	}
	if def.ServiceSchema == nil || def.ServiceSchema.ServiceTemplate == "" {
		t.Fatal("systemd-container has no service_schema.service_template; the inherited " +
			"template is missing, so every custom service entry fails to render " +
			"(service_schema is inherited as ONE field: a child must leave it empty)")
	}
	if def.ServiceSchema.ServiceTemplate != base.ServiceSchema.ServiceTemplate {
		t.Error("systemd-container's service_template is not the inherited systemd: template")
	}
}

// TestEmbeddedVocab_SystemdContainerEnablesPackagedSystemUnits exercises the
// container init's system_enable_template. It is a SECOND enable path beside
// assembly_template: the assembly enables candy-SHIPPED unit files, while this
// template enables a distro-PACKAGED unit a service: entry names with
// use_packaged. A template nobody renders is dead, and an EMPTY one emits a
// bare `RUN` that fails the build, so both are asserted here rather than left
// to a bed whose fixture may ship no packaged units.
func TestEmbeddedVocab_SystemdContainerEnablesPackagedSystemUnits(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil || ic.Init["systemd-container"] == nil {
		t.Fatal("embedded vocabulary missing the systemd-container init def")
	}
	def := ic.Init["systemd-container"]
	if def.SystemEnableTemplate == "" {
		t.Fatal("systemd-container declares no system_enable_template — a packaged " +
			"system-scope service would never be enabled")
	}
	// Render the authored template directly: the sdk mechanism kits are for
	// PLUGINS, never the host (the import-purity gate), and this template reads
	// only .Units, so text/template produces the same text the SDK renderer does.
	tmpl, err := template.New("system-enable").Parse(def.SystemEnableTemplate)
	if err != nil {
		t.Fatalf("system_enable_template does not parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Units": []string{"w38-packaged.service"}}); err != nil {
		t.Fatalf("rendering system_enable_template: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "systemctl enable w38-packaged.service") {
		t.Errorf("the rendered system-enable block does not enable the packaged unit:\n%s", got)
	}
	if strings.Contains(got, "RUN\n") {
		t.Error("the rendered system-enable block emitted a bare RUN; an empty template " +
			"would fail the build")
	}
}
