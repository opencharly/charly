package main

import (
	"context"
	"fmt"

	"github.com/alecthomas/kong"
)

// CommandProvider is the typed in-process form of a COMMAND-class Provider: it
// contributes one or more Kong subcommands to the charly CLI root. KongCommand
// returns a POINTER to a struct whose fields are merged into the Kong grammar via
// kong.Plugins (the CLI struct embeds kong.Plugins); the embedded command type
// carries the Run(...) handler Kong dispatches to. This is the 6th provider class
// (kind/verb/deploy/step/builder/command). Only NON-machinery commands become
// providers; the machinery commands (box/migrate/__plugin/version) stay hardcoded
// on the CLI struct.
type CommandProvider interface {
	Provider
	KongCommand() any
}

// builtinCommandBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in command provider. A compiled-in command contributes via
// KongCommand + its Go Run handler; it does not serve itself out-of-process.
type builtinCommandBase struct{}

func (builtinCommandBase) Class() ProviderClass { return ClassCommand }
func (builtinCommandBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in command is in-process only (no out-of-proc Invoke)")
}

// collectCommandPlugins enumerates every registered CommandProvider and returns their
// Kong command structs for kong.Plugins embedding on the CLI root. Order is stable
// (allProviders sorts by registry key), so the grammar is deterministic. Empty until
// commands are extracted into providers (Phase 1-4 migrations) — the seam is
// dormant-but-tested until then (TestCommandSeam_PluginCommandInjected).
func collectCommandPlugins() kong.Plugins {
	var plugins kong.Plugins
	for _, p := range providerRegistry.allProviders() {
		if cp, ok := p.(CommandProvider); ok {
			plugins = append(plugins, cp.KongCommand())
		}
	}
	return plugins
}
