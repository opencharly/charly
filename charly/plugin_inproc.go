package main

import (
	"context"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

// inprocProvider is a Provider backed by a COMPILED-IN plugin candy's
// pb.ProviderServer, called IN-PROCESS — the in-proc twin of grpcProvider (which
// calls the SAME pb.ProviderServer methods over gRPC). Call sites never distinguish
// the two: placement (compiled-in vs out-of-process) is invisible above the
// registry. A plugin candy serves ONE provider that works in BOTH placements; this
// type is how the in-proc placement reaches it without a socket.
type inprocProvider struct {
	srv        pb.ProviderServer
	class      ProviderClass
	word       string
	contract   *stepContract // set ONLY for a compiled-in class:step capability declaring a StepContract (F3); nil otherwise
	structural bool          // set ONLY for a compiled-in class:kind capability that decodes a STRUCTURAL entity (F5)
	validates  bool          // set ONLY for a compiled-in class:kind capability serving a deep OpValidate check (F7/C8)
	phase      string        // the plugin lifecycle phase (F9; sdk.Phase*, normalized — "" → runtime)
}

func (p *inprocProvider) Reserved() string     { return p.word }
func (p *inprocProvider) Class() ProviderClass { return p.class }

// declaredStepContract implements stepContractCarrier — the in-proc twin of
// grpcProvider.declaredStepContract, so a COMPILED-IN class:step plugin carries the SAME
// declared Scope/Venue/Gate (R3: placement-invisible above the registry).
func (p *inprocProvider) declaredStepContract() (stepContract, bool) {
	if p.contract == nil {
		return stepContract{}, false
	}
	return *p.contract, true
}

// isStructuralKind implements structuralKindCarrier — the in-proc twin of
// grpcProvider.isStructuralKind, so a COMPILED-IN class:kind plugin folds to uf.Bundle the
// SAME way (R3: placement-invisible).
func (p *inprocProvider) isStructuralKind() bool { return p.structural }

// isValidatingKind implements validatingKindCarrier — the in-proc twin of
// grpcProvider.isValidatingKind (R3 parity).
func (p *inprocProvider) isValidatingKind() bool { return p.validates }

// pluginPhase implements phaseCarrier — the in-proc twin of grpcProvider.pluginPhase (F9, R3 parity).
func (p *inprocProvider) pluginPhase() string { return p.phase }

func (p *inprocProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := p.srv.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(p.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// buildUnitInProc lifts a compiled-in plugin's (meta, provider) pair into a
// *PluginUnit by calling Describe IN-PROCESS and wrapping each advertised
// capability in an inprocProvider — the in-proc analogue of buildUnit
// (plugin_grpc.go), applying the SAME protocol-version gate and the SAME capability
// validation (R3: one capability-lifting contract, two transports). The candy's
// Describe is the single schema source for both placements, so the host's
// load/gate/validate path is byte-identical whether the plugin is compiled in or
// served out-of-process.
func buildUnitInProc(meta pb.PluginMetaServer, srv pb.ProviderServer) (*PluginUnit, error) {
	caps, err := meta.Describe(context.Background(), &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("compiled-in plugin describe: %w", err)
	}
	if caps.GetProtocolVersion() != sdk.ProtocolVersion {
		return nil, fmt.Errorf("compiled-in plugin protocol version mismatch: plugin advertises protocol %d (CalVer %q), host requires protocol %d",
			caps.GetProtocolVersion(), caps.GetCalver(), sdk.ProtocolVersion)
	}
	provided := caps.GetProvided()
	providers := make([]Provider, 0, len(provided))
	inputDefs := make(map[string]string, len(provided))
	for _, c := range provided {
		class := ProviderClass(c.GetClass())
		if !providerClasses[class] || c.GetWord() == "" {
			return nil, fmt.Errorf("compiled-in plugin advertised malformed capability %q:%q", c.GetClass(), c.GetWord())
		}
		ip := &inprocProvider{srv: srv, class: class, word: c.GetWord()}
		// A compiled-in class:step capability carries its declared StepContract too (F3) —
		// the in-proc twin of buildUnit's grpcProvider population (R3, placement parity).
		if sc := c.GetStepContract(); class == ClassStep && sc != nil {
			ip.contract = &stepContract{Scope: scopeFromName(sc.GetScope()), Venue: Venue(sc.GetVenue()), Gate: Gate(sc.GetGate()), Emits: sc.GetEmits()}
		}
		// A compiled-in class:kind capability carries its STRUCTURAL flag too (F5, R3 parity).
		if class == ClassKind && c.GetStructural() {
			ip.structural = true
		}
		// ...and its VALIDATES flag (F7/C8, R3 parity).
		if class == ClassKind && c.GetValidates() {
			ip.validates = true
		}
		// ...and its lifecycle PHASE (F9, R3 parity; normalized, default runtime).
		ip.phase = sdk.NormalizePhase(c.GetPhase())
		providers = append(providers, ip)
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return &PluginUnit{
		Providers: providers,
		Schema:    PluginSchema{CueSource: caps.GetSchemaCue(), InputDefs: inputDefs},
	}, nil
}

// registerCompiledPlugin registers a COMPILED-IN plugin candy's provider in-process.
// Called from the generated plugins_generated.go init() for each candy in the
// charly.yml `compiled_plugins:` selection. It reuses RegisterBuiltinPluginUnit, so
// the compiled-in candy enters the SAME builtinPluginUnits gate (schema gated at
// process start) and registers with origin "builtin" — the fact the coexist switch
// in loadProjectPlugins keys on to skip the host go-build + out-of-process connect
// for an already-compiled-in word. A Describe/schema failure here is a build-time
// invariant violation (the candy is compiled into THIS binary), so it panics,
// mirroring loadBuiltinPluginUnits' fail-loud-at-startup contract.
func registerCompiledPlugin(srv pb.ProviderServer, meta pb.PluginMetaServer) {
	unit, err := buildUnitInProc(meta, srv)
	if err != nil {
		panic("registerCompiledPlugin: " + err.Error())
	}
	RegisterBuiltinPluginUnit(*unit)
}
