package main

// kit_reverse_aliases.go — package-main bindings onto the reverse-op teardown
// machinery (reverse_ops.go), moved to sdk/kit in P4. The 4 ReverseExecutor
// flag-accessor methods are EXPORTED (kit.ReverseExecutor) so charly types
// (BundleDelCmd here, hostReverseExec in deploy_host_helpers.go) can satisfy the
// interface across the package boundary. The DistroConfig-dependent uninstall
// render is injected into FillReverseUninstallCmds via a callback (the charly
// caller in deploy_target_external.go closes over DistroCfg + RenderTemplate).

import "github.com/opencharly/sdk/kit"

type (
	ReverseExecutor = kit.ReverseExecutor
	ReverseRunner   = kit.ReverseRunner
)

var (
	runReverseOps            = kit.RunReverseOps
	fillReverseUninstallCmds = kit.FillReverseUninstallCmds

	// RemoveManagedBlockAt is kit/profile's file-level managed-block remover, used
	// by the reverse-managed-block teardown (kit) + host_infra_test.go.
	RemoveManagedBlockAt    = kit.RemoveManagedBlockAt
	renderManagedBlockStrip = kit.RenderManagedBlockStrip
)

// BundleDelCmd satisfies kit.ReverseExecutor via thin wrappers — keeps the
// flag-accessor protocol decoupled from the concrete command type.
func (c *BundleDelCmd) ReverseDryRun() bool          { return c.DryRun }
func (c *BundleDelCmd) ReverseKeepRepoChanges() bool { return c.KeepRepoChanges }
func (c *BundleDelCmd) ReverseKeepServices() bool    { return c.KeepServices }
func (c *BundleDelCmd) ReverseRunner() ReverseRunner { return c.Runner }
