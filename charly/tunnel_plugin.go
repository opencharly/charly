package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// tunnel_plugin.go is the CORE adapter for the externalized tunnel EXECUTION leg (the
// C16b core-externalization cutover). The tailscale serve/funnel commands + the entire
// cloudflared tunnel lifecycle live OUT-OF-PROCESS in candy/plugin-tunnel (verb:tunnel);
// charly/tunnel.go keeps only the pure RESOLUTION (ResolveTunnelConfig /
// TunnelConfigFromMetadata) + the quadlet-shared helpers. Every core tunnel consumer
// (start.go's TunnelStart/TunnelStop, config_image.go's cloudflareTunnelSetup) is
// UNCHANGED — it resolves the same *TunnelConfig and calls the same seams below, which
// forward to verb:tunnel over the provider registry (compiled into charly via
// `compiled_plugins:`, or the out-of-process coexist path when composed from source).
//
// The wire form matches the check-step path: both wrap the operation in a `plugin_input`
// envelope carrying {method, config}, so the plugin (candy/plugin-tunnel) has ONE decode
// path whether the caller is this adapter (start/stop/setup) or a `plugin: tunnel` check
// step (method: plan). This mirrors credential_plugin.go's welded-verb adapter pattern.

// tunnelWireInput is the {method, config} payload sent to verb:tunnel, byte-compatible
// with candy/plugin-tunnel's params.TunnelInput (method + config; the plan-only `expect`
// field is authored on a check step, never sent by this adapter).
type tunnelWireInput struct {
	Method string        `json:"method"`
	Config *TunnelConfig `json:"config,omitempty"`
}

// tunnelReply is the wire form verb:tunnel's exec methods (start/stop/setup) return,
// byte-compatible with candy/plugin-tunnel's tunnelReply.
type tunnelReply struct {
	Error      string `json:"error,omitempty"`
	Name       string `json:"name,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
}

// tunnelProvider resolves verb:tunnel. Registry-first so a COMPILED-IN plugin
// (registered at init via registerCompiledPlugin) resolves in-process AND project-lessly
// — the deploy-time callers (charly start / charly config with a tunnel) run on hosts that
// may carry no project. It falls back to connectPluginByWord for the baked / project-source
// coexist paths (an installed /usr/lib/charly/plugins binary, or a project composing the
// candy from source).
func tunnelProvider() (Provider, bool) {
	if p, ok := providerRegistry.resolve(ClassVerb, "tunnel"); ok {
		return p, true
	}
	return connectPluginByWord(ClassVerb, "tunnel")
}

// invokeTunnel forwards one tunnel operation to verb:tunnel and decodes the tunnelReply.
func invokeTunnel(cfg TunnelConfig, method string) (tunnelReply, error) {
	prov, ok := tunnelProvider()
	if !ok {
		return tunnelReply{}, fmt.Errorf(
			"tunnel plugin (verb:tunnel) did not connect — candy/plugin-tunnel is compiled into charly " +
				"(compiled_plugins) by default; on a custom build install it alongside charly " +
				"(/usr/lib/charly/plugins) or run from a project composing it")
	}
	in := tunnelWireInput{Method: method, Config: &cfg}
	params, err := marshalJSON(map[string]any{"plugin_input": in})
	if err != nil {
		return tunnelReply{}, err
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "tunnel", Op: OpRun, Params: params})
	if err != nil {
		return tunnelReply{}, err
	}
	var r tunnelReply
	if err := json.Unmarshal(out.JSON, &r); err != nil {
		return tunnelReply{}, fmt.Errorf("decode tunnel reply: %w", err)
	}
	return r, nil
}

// TunnelStart dispatches to verb:tunnel's `start` method. Package-level var for
// testability (same seam pattern the pre-externalization code kept).
var TunnelStart = defaultTunnelStart

func defaultTunnelStart(cfg TunnelConfig) error {
	r, err := invokeTunnel(cfg, "start")
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// TunnelStop dispatches to verb:tunnel's `stop` method.
var TunnelStop = defaultTunnelStop

func defaultTunnelStop(cfg TunnelConfig) error {
	r, err := invokeTunnel(cfg, "stop")
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// cloudflareTunnelSetup forwards to verb:tunnel's `setup` method — create the tunnel,
// write the cloudflared config YAML, route DNS — returning the tunnel name + config path
// (config_image.go's quadlet path discards them; the signature is retained so callers
// compile unchanged).
func cloudflareTunnelSetup(cfg TunnelConfig) (tunnelName, configPath string, err error) {
	r, ierr := invokeTunnel(cfg, "setup")
	if ierr != nil {
		return "", "", ierr
	}
	if r.Error != "" {
		return "", "", errors.New(r.Error)
	}
	return r.Name, r.ConfigPath, nil
}
