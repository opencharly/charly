package main

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// plugin_provider_common.go — the shared capability metadata + capability-lift loop the two
// Provider placements build on. Everything byte-identical between the out-of-process grpcProvider
// and the compiled-in inprocProvider lives here ONCE (R3); each placement keeps only its
// transport-specific extras (grpcProvider: conn + broker + lifecycle/preresolve wiring + the
// executorInvoker reverse channel; inprocProvider: the in-proc pb.ProviderServer invoke).

// capMeta is the shared capability metadata every Provider carries regardless of placement — the
// fields + carrier methods that are identical for grpcProvider and inprocProvider. Both EMBED it,
// so the carrier interfaces (spec.StepContractCarrier / spec.StructuralKindCarrier /
// spec.ValidatingKindCarrier / spec.PhaseCarrier / primaryCarrier) are satisfied ONCE for both
// placements via method promotion. The carrier interfaces + StepContract live in sdk/spec (K4-C
// relocation, provider_carriers.go) — an interface with an unexported method can only be
// satisfied by a type in the SAME package as the interface, so capMeta's carrier methods below
// are EXPORTED to satisfy them from across the package boundary.
type capMeta struct {
	class            ProviderClass
	word             string
	contract         *spec.StepContract  // set ONLY for a class:step capability declaring a StepContract (F3); nil otherwise
	structural       bool                // set ONLY for a class:kind capability that decodes a STRUCTURAL entity (F5)
	validates        bool                // set ONLY for a class:kind capability serving a deep OpValidate check (F7/C8)
	phase            string              // the plugin lifecycle phase (F9; sdk.Phase*, normalized — "" → runtime)
	primary          string              // set ONLY for a class:verb capability declaring a scalar-sugar primary input field
	traits           *spec.DeployTraits  // set ONLY for a SUBSTRATE class:kind capability declaring #DeployTraits (P9); nil otherwise
	cmdParent        string              // set ONLY for a COMPILED-IN class:command capability nesting under a parent command word (e.g. "box" for `charly box generate`); "" → a top-level command
	subcmds          []sdk.CLISubcommand // set ONLY for a class:command capability declaring a subcommand catalog (F-CLI-NEST); empty → the flat pass-through holder
	commandModel     *spec.CLIModel      // set ONLY for class:command; CUE-generated reflected leaf grammar
	commandModelJSON []byte              // exact validated transport payload, preserved across relays
}

func (m capMeta) Reserved() string     { return m.word }
func (m capMeta) Class() ProviderClass { return m.class }

// CommandParent implements NestedCommandProvider (provider_command_external.go) — the parent
// command word this command nests UNDER (e.g. "box" for `charly box generate`), or "" for a
// top-level command. Both provider twins EMBED capMeta, so both satisfy NestedCommandProvider via
// promotion; the VALUE is set only for a COMPILED-IN class:command capability whose provider
// declares it via the optional CommandParent() interface (buildUnitInProc). Every other capability
// — and every OUT-OF-PROCESS command — returns "" (top-level; no live out-of-process nested command
// exists), so collectExternalCommandPlugins's `parent != ""` guard is what actually gates nesting.
func (m capMeta) CommandParent() string { return m.cmdParent }

// CommandModel returns a defensive copy of the plugin-published generated
// #CLIModel. It lets the host merge plugin-owned leaves into __cli-model.
func (m capMeta) CommandModel() *spec.CLIModel {
	if m.commandModel == nil {
		return nil
	}
	copy := *m.commandModel
	copy.Leaves = append([]spec.CLILeaf(nil), m.commandModel.Leaves...)
	return &copy
}

func (m capMeta) commandModelPayload() []byte {
	return append([]byte(nil), m.commandModelJSON...)
}

// DeclaredStepContract implements spec.StepContractCarrier — a class:step capability's
// plugin-declared Scope/Venue/Gate/Emits (F3), nil/false for every other capability.
func (m capMeta) DeclaredStepContract() (spec.StepContract, bool) {
	if m.contract == nil {
		return spec.StepContract{}, false
	}
	return *m.contract, true
}

// IsStructuralKind implements spec.StructuralKindCarrier — a class:kind capability whose decode
// returns a spec.Deploy member tree (-> uf.Bundle) rather than a flat body (F5).
func (m capMeta) IsStructuralKind() bool { return m.structural }

// IsValidatingKind implements spec.ValidatingKindCarrier — a class:kind capability serving a deep
// OpValidate check the host dispatches at load (F7/C8).
func (m capMeta) IsValidatingKind() bool { return m.validates }

// PluginPhase implements spec.PhaseCarrier — the plugin lifecycle phase the kernel loads/invokes
// this capability in (F9; normalized, never empty).
func (m capMeta) PluginPhase() string { return m.phase }

// primaryInput implements primaryCarrier — a class:verb capability's declared scalar-sugar primary
// input field (empty for every other capability).
func (m capMeta) primaryInput() string { return m.primary }

// DeclaredDeployTraits implements spec.DeployTraitsCarrier — a SUBSTRATE class:kind capability's
// declared #DeployTraits (P9), nil for every other capability. deployTraitsFor reads it off the
// registry so kit.StampDescent can stamp node.Descent BY TRAIT, never by kind-word switch.
func (m capMeta) DeclaredDeployTraits() *spec.DeployTraits { return m.traits }

// declaredSubcommands implements commandSubcommandCarrier (provider_command_external.go) — a
// class:command capability's DECLARED one-level-deep CLI subcommand catalog (F-CLI-NEST), empty
// for every capability that doesn't declare one (preserving today's flat pass-through holder).
func (m capMeta) declaredSubcommands() []sdk.CLISubcommand { return m.subcmds }

// buildCapMeta lifts one advertised pb.ProvidedCapability into the shared capMeta both provider
// twins embed — the class/word plus the class-gated contract/structural/validates/phase/primary
// flags, applied IDENTICALLY regardless of placement (R3). The caller (liftCapabilities) has
// already validated the class + word are well-formed.
func buildCapMeta(c *pb.ProvidedCapability) (capMeta, error) {
	m := capMeta{class: ProviderClass(c.GetClass()), word: c.GetWord()}
	if raw := c.GetCommandModelJson(); len(raw) > 0 {
		if m.class != ClassCommand {
			return capMeta{}, fmt.Errorf("capability %s:%s carries command_model_json outside class=command", m.class, m.word)
		}
		var model spec.CLIModel
		if err := json.Unmarshal(raw, &model); err != nil {
			return capMeta{}, fmt.Errorf("command %s model: %w", m.word, err)
		}
		if err := sdk.ValidateGenerated("#CLIModel", model); err != nil {
			return capMeta{}, fmt.Errorf("command %s model: %w", m.word, err)
		}
		m.commandModel = &model
		m.commandModelJSON = append([]byte(nil), raw...)
	}
	// A class:step capability may DECLARE its install-step contract (F3): compileActOp builds an
	// externalStep carrying the plugin-declared Scope/Venue/Gate/Emits.
	if sc := c.GetStepContract(); m.class == ClassStep && sc != nil {
		m.contract = &spec.StepContract{Scope: spec.ScopeFromName(sc.GetScope()), Venue: spec.Venue(sc.GetVenue()), Gate: spec.Gate(sc.GetGate()), Emits: sc.GetEmits()}
	}
	// A class:kind capability may declare it decodes a STRUCTURAL entity (F5): runPluginKind folds
	// its spec.Deploy reply into uf.Bundle instead of landing a flat body opaquely.
	if m.class == ClassKind && c.GetStructural() {
		m.structural = true
	}
	// A class:kind capability may declare a deep OpValidate check (F7/C8).
	if m.class == ClassKind && c.GetValidates() {
		m.validates = true
	}
	// Every capability declares a lifecycle PHASE (F9; normalized, default runtime).
	m.phase = sdk.NormalizePhase(c.GetPhase())
	if m.class == ClassVerb {
		m.primary = c.GetPrimary()
	}
	// A class:command capability may DECLARE a one-level-deep subcommand catalog (F-CLI-NEST):
	// collectExternalCommandPlugins builds a REAL nested Kong holder from it instead of the flat
	// pass-through, and buildCLIModel synthesizes a "<word>.<name>" leaf per entry for MCP.
	if m.class == ClassCommand {
		for _, sc := range c.GetSubcommands() {
			m.subcmds = append(m.subcmds, sdk.CLISubcommand{Name: sc.GetName(), Help: sc.GetHelp()})
		}
	}
	// A SUBSTRATE class:kind capability may declare #DeployTraits (P9): kit.StampDescent stamps
	// them onto node.Descent so the deploy behaviour is consulted BY TRAIT, not by kind word.
	if m.class == ClassKind {
		if dt := c.GetDeployTraits(); dt != nil {
			m.traits = &spec.DeployTraits{
				Venue:          dt.GetVenue(),
				ImageBacked:    dt.GetImageBacked(),
				ImageContext:   dt.GetImageContext(),
				MachineVenue:   dt.GetMachineVenue(),
				ExclusiveVenue: dt.GetExclusiveVenue(),
				LeafOnly:       dt.GetLeafOnly(),
			}
		}
	}
	return m, nil
}

// liftCapabilities is the ONE capability-lift loop both buildUnit (out-of-process) and
// buildUnitInProc (compiled-in) share (R3): it validates each advertised capability, builds its
// shared capMeta, hands that to the placement-specific newProvider factory (which adds the grpc
// conn + lifecycle/preresolve extras, or the in-proc pb.ProviderServer), and collects the
// per-capability input defs. `origin` labels the placement in the malformed-capability error.
func liftCapabilities(provided []*pb.ProvidedCapability, origin string, newProvider func(capMeta, *pb.ProvidedCapability) Provider) ([]Provider, map[string]string, error) {
	providers := make([]Provider, 0, len(provided))
	inputDefs := make(map[string]string, len(provided))
	for _, c := range provided {
		class := ProviderClass(c.GetClass())
		if !providerClasses[class] || c.GetWord() == "" {
			return nil, nil, fmt.Errorf("%s advertised malformed capability %q:%q", origin, c.GetClass(), c.GetWord())
		}
		meta, err := buildCapMeta(c)
		if err != nil {
			return nil, nil, fmt.Errorf("%s advertised invalid capability %s:%s: %w", origin, c.GetClass(), c.GetWord(), err)
		}
		providers = append(providers, newProvider(meta, c))
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return providers, inputDefs, nil
}
