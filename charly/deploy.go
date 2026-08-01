package main

import (
	"github.com/opencharly/spec/spec"
)

// deploy.go — the DeployConfigPath/DeployConfigEnv charly.yml-path seam pointers.
//
// The deploy-key→image RESOLVERS that once lived here (the per-host + PROJECT deploy-key→box
// loader-read + the resolved_image overlay preference) MOVED into candy/plugin-deploy-pod
// (resolve_ref.go's resolveDeployRefLocal) in #55 Cone A Unit 2, the deploy-config-resolve
// reverse-leg SEAM COLLAPSE. The pod plugin now self-resolves plugin-side (per-host via the
// deploy-config load seam + PROJECT via the shared loaderkit.LoadUnifiedViaExecutor helper), so
// the host seam and these host-side resolvers are gone — deploy.go no longer imports sdk/deploykit
// or sdk/spec. The check-plumbing consumer resolves the box name INLINE from the project + overlay
// it already loads — plugin-side now, in candy/plugin-check/members.go (resolveDeployBoxName). See
// each repo's CHANGELOG/ for the collapse (retired names).
//
// The deploy STATE-MODEL body (LoadBundleConfig / SaveBundleConfig / LoadDeployConfigForRead /
// LoadDeployConfigForWrite / MergeDeployOntoMetadata / CleanDeployEntry / SaveDeployState /
// ExportAllBox + the pure helpers) had already MOVED to sdk/deploykit in K5-Unit-1.

// DeployConfigPath returns the path to the deploy overlay file. Package-level var for
// testability (tests inject a temp path, same pattern as RuntimeConfigPath). The resolver
// body lives in spec.DefaultDeployConfigPath — ONE definition shared with the out-of-module
// candy/plugin-migrate (R3).
var DeployConfigPath = spec.DefaultDeployConfigPath

// DeployConfigEnv overrides the per-host deploy-config PATH. A check bed sets it (via the
// bed runner) to a PER-BED isolated file so CONCURRENT beds never share — and corrupt —
// the operator's ~/.config/charly/charly.yml, and a disposable bed's transient
// resolved_port/quadlet state never pollutes the operator's persistent config. The 2026-07
// maxjobs-load corruption (`node "…": kind:group: #GroupInput.resolved_port: field not
// allowed`) was concurrent beds racing the shared read-modify-write of this one file.
const DeployConfigEnv = spec.DeployConfigEnv
