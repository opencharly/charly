package main

import (
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// plugin_provider_common.go — the shared capability metadata + capability-lift loop the two
// Provider placements build on. Everything byte-identical between the out-of-process grpcProvider
// and the compiled-in inprocProvider lives here ONCE (R3); each placement keeps only its
// transport-specific extras (grpcProvider: conn + broker + lifecycle/preresolve wiring + the
// executorInvoker reverse channel; inprocProvider: the in-proc pb.ProviderServer invoke).

// capMeta is the shared capability metadata every Provider carries regardless of placement — the
// fields + carrier methods that are identical for grpcProvider and inprocProvider. Both EMBED it,
// so the carrier interfaces (stepContractCarrier / structuralKindCarrier / validatingKindCarrier /
// phaseCarrier / primaryCarrier) are satisfied ONCE for both placements via method promotion.
type capMeta struct {
	class      ProviderClass
	word       string
	contract   *stepContract // set ONLY for a class:step capability declaring a StepContract (F3); nil otherwise
	structural bool          // set ONLY for a class:kind capability that decodes a STRUCTURAL entity (F5)
	validates  bool          // set ONLY for a class:kind capability serving a deep OpValidate check (F7/C8)
	phase      string        // the plugin lifecycle phase (F9; sdk.Phase*, normalized — "" → runtime)
	primary    string        // set ONLY for a class:verb capability declaring a scalar-sugar primary input field
}

func (m capMeta) Reserved() string     { return m.word }
func (m capMeta) Class() ProviderClass { return m.class }

// declaredStepContract implements stepContractCarrier — a class:step capability's plugin-declared
// Scope/Venue/Gate/Emits (F3), nil/false for every other capability.
func (m capMeta) declaredStepContract() (stepContract, bool) {
	if m.contract == nil {
		return stepContract{}, false
	}
	return *m.contract, true
}

// isStructuralKind implements structuralKindCarrier — a class:kind capability whose decode returns
// a spec.Deploy member tree (-> uf.Bundle) rather than a flat body (F5).
func (m capMeta) isStructuralKind() bool { return m.structural }

// isValidatingKind implements validatingKindCarrier — a class:kind capability serving a deep
// OpValidate check the host dispatches at load (F7/C8).
func (m capMeta) isValidatingKind() bool { return m.validates }

// pluginPhase implements phaseCarrier — the plugin lifecycle phase the kernel loads/invokes this
// capability in (F9; normalized, never empty).
func (m capMeta) pluginPhase() string { return m.phase }

// primaryInput implements primaryCarrier — a class:verb capability's declared scalar-sugar primary
// input field (empty for every other capability).
func (m capMeta) primaryInput() string { return m.primary }

// buildCapMeta lifts one advertised pb.ProvidedCapability into the shared capMeta both provider
// twins embed — the class/word plus the class-gated contract/structural/validates/phase/primary
// flags, applied IDENTICALLY regardless of placement (R3). The caller (liftCapabilities) has
// already validated the class + word are well-formed.
func buildCapMeta(c *pb.ProvidedCapability) capMeta {
	m := capMeta{class: ProviderClass(c.GetClass()), word: c.GetWord()}
	// A class:step capability may DECLARE its install-step contract (F3): compileActOp builds an
	// externalStep carrying the plugin-declared Scope/Venue/Gate/Emits.
	if sc := c.GetStepContract(); m.class == ClassStep && sc != nil {
		m.contract = &stepContract{Scope: scopeFromName(sc.GetScope()), Venue: Venue(sc.GetVenue()), Gate: Gate(sc.GetGate()), Emits: sc.GetEmits()}
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
	return m
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
		providers = append(providers, newProvider(buildCapMeta(c), c))
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return providers, inputDefs, nil
}
