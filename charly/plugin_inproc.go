package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// inprocProvider is a Provider backed by a COMPILED-IN plugin candy's
// pb.ProviderServer, called IN-PROCESS — the in-proc twin of grpcProvider (which
// calls the SAME pb.ProviderServer methods over gRPC). Call sites never distinguish
// the two: placement (compiled-in vs out-of-process) is invisible above the
// registry. A plugin candy serves ONE provider that works in BOTH placements; this
// type is how the in-proc placement reaches it without a socket. It embeds capMeta (the shared
// class/word + carrier methods, R3) and adds ONLY the in-proc pb.ProviderServer; it deliberately
// does NOT carry a lifecycle/preresolve flag or InvokeWithExecutor (those are grpc-only — a
// compiled-in provider must not satisfy the executorInvoker discriminator).
type inprocProvider struct {
	capMeta
	srv pb.ProviderServer
}

func (p *inprocProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := p.srv.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(p.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

func (p *inprocProvider) OpenChannel(open *pb.ChannelFrame, stream sdk.ProviderChannel) error {
	channel, ok := p.srv.(sdk.ChannelProvider)
	if !ok {
		return fmt.Errorf("compiled-in provider %s:%s has no bidirectional channel", p.class, p.word)
	}
	return channel.OpenChannel(open, stream)
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
	// The capability-lift loop is shared with buildUnit via liftCapabilities (R3): the compiled-in
	// factory wraps the SAME capMeta in an inprocProvider (its only extra is the in-proc
	// pb.ProviderServer). Placement is invisible above the registry.
	providers, inputDefs, err := liftCapabilities(caps.GetProvided(), "compiled-in plugin", func(meta capMeta, _ *pb.ProvidedCapability) Provider {
		// A COMPILED-IN command candy may NEST its command(s) under a parent command word
		// (e.g. candy/plugin-box's generate/validate/… under `box`). The parent rides an
		// optional Go interface on the plugin's own provider (the SAME srv-interface-detection
		// pattern registerCompiledPlugin uses for spec.DocParser / kit.RefsDownloader), so
		// the compiled-in inprocProvider surfaces it via capMeta.CommandParent() and
		// collectExternalCommandPlugins nests the dynamic Kong subcommand under that parent. This
		// is the compiled-in placement of nesting; an out-of-process command declares no parent
		// (top-level) — no live out-of-process nested command exists.
		if meta.class == ClassCommand {
			if ncp, ok := srv.(interface{ CommandParent() string }); ok {
				meta.cmdParent = ncp.CommandParent()
			}
		}
		return &inprocProvider{capMeta: meta, srv: srv}
	})
	if err != nil {
		return nil, err
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
	// A compiled-in loader plugin (P6) exposes the typed per-document PARSE via spec.DocParser
	// — wire it as the active config front-end so the host calls it directly (no wire envelope) per
	// document. The provider's Invoke stays registered for the out-of-process placement. There is no
	// in-core fallback parser (K1 deleted loaderkit.DefaultParser): the compiled-in loader plugin is
	// the sole parser, registered here at init before any load; requireLoaderParser FATALs if absent.
	if dp, ok := srv.(spec.DocParser); ok {
		activeLoaderParser = dp
	}
	// The SAME compiled-in loader plugin (#46) ALSO exposes the typed whole-project WALK via
	// spec.ProjectWalker — wire it as the active walk mechanism so hostWalkProject dispatches
	// through it (no wire envelope), instead of charly core importing sdk/loaderkit directly.
	if pw, ok := srv.(spec.ProjectWalker); ok {
		activeProjectWalker = pw
	}
	// The SAME compiled-in loader plugin (W9) ALSO exposes the typed CANDY-SCAN via
	// spec.CandyScanner — wire it as the active scan mechanism so ScanAllCandy dispatches through
	// it (no wire envelope), instead of charly core holding the scan+construct logic itself.
	if cs, ok := srv.(spec.CandyScanner); ok {
		activeCandyScanner = cs
	}
	// A compiled-in refs plugin (P7) exposes the typed remote-repo DOWNLOAD via kit.RefsDownloader —
	// wire it as the active fetch backend so EnsureRepoDownloaded dispatches every cache-miss download
	// through it (no wire envelope). See candy/plugin-refs.
	if rd, ok := srv.(kit.RefsDownloader); ok {
		activeRefsDownloader = rd
	}
}
