// Package service is the importable, COMPILED-IN host-coupled `service` verb: the
// TYPED-STEP state-provision verb, THREE-natured on the sdk/kit contract:
//   - CheckVerbProvider (do:assert): probe running/enabled via supervisorctl/systemctl
//     through the live kit.CheckContext.
//   - ProvisionActor (do:act runtime): render the systemctl/supervisorctl enable shell.
//   - StepProvider (do:act build/deploy install timeline): lower into a ServicePackagedStep
//     (the host materializes the descriptor, keeping the load-bearing Reverse() in package
//     main). Relocated out of charly's module (formerly charly/plugin/builtins/service +
//     charly/plugin_verb_service.go); COMPILED-IN-ONLY.
package service

import (
	"context"
	"embed"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-service/params"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:service": "#ServiceInput"}

// NewCheckVerb returns the service verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor + kit.StepProvider, charly
// registers the three-role (check + act + typed-step) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "service" }

// RunVerb (do:assert) probes running/enabled via the live CheckContext. Mirrors r.runService:
// supervisorctl status first (the disposable pods run supervisord), systemctl is-active/
// is-enabled fallback. A supervisord service is "enabled" while supervisord is up.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.ServiceInput
	kit.DecodeInput(op.PluginInput, &in)
	svc := kit.ShellQuote(in.Service)
	if in.Running != nil {
		probe := fmt.Sprintf(`supervisorctl status %[1]s 2>/dev/null | grep -q RUNNING || systemctl is-active --quiet %[1]s`, svc)
		_, _, exit, err := cc.Exec().RunCapture(ctx, probe)
		if err != nil {
			return kit.Failf("running probe: %v", err)
		}
		isRunning := exit == 0
		if isRunning != *in.Running {
			return kit.Failf("running=%v, want %v", isRunning, *in.Running)
		}
	}
	if in.Enabled != nil {
		probe := fmt.Sprintf(`supervisorctl status %[1]s 2>/dev/null | grep -qE '(RUNNING|STARTING|STOPPED)' || systemctl is-enabled --quiet %[1]s`, svc)
		_, _, exit, _ := cc.Exec().RunCapture(ctx, probe)
		isEnabled := exit == 0
		if isEnabled != *in.Enabled {
			return kit.Failf("enabled=%v, want %v", isEnabled, *in.Enabled)
		}
	}
	return kit.Pass("ok")
}

// RenderProvisionScript (do:act runtime) renders the enable + start under whichever init
// the live target runs. ok is always true — a service act always has an enable form.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.ServiceInput
	kit.DecodeInput(op.PluginInput, &in)
	svc := kit.ShellQuote(in.Service)
	return fmt.Sprintf(`if command -v systemctl >/dev/null 2>&1; then systemctl enable --now %[1]s; `+
		`elif command -v supervisorctl >/dev/null 2>&1; then supervisorctl start %[1]s; `+
		`else echo "no service manager" >&2; exit 1; fi`, svc), true
}

// StepKind names the typed install-plan step service's build/deploy act lowers into.
func (verb) StepKind() kit.StepKindName { return kit.StepKindServicePackaged }

// ConstructStepDescriptor (do:act build/deploy) returns the candy-decodable inputs for the
// ServicePackagedStep — enable the named packaged unit. The host materializer adds the
// op-resolved scope + candy name and keeps the load-bearing Reverse().
func (verb) ConstructStepDescriptor(op *spec.Op) kit.StepDescriptor {
	var in params.ServiceInput
	kit.DecodeInput(op.PluginInput, &in)
	return kit.StepDescriptor{ServicePackaged: &kit.ServicePackagedDesc{Unit: in.Service, Enable: true}}
}
