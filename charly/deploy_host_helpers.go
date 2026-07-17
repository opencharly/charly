package main

// deploy_host_helpers.go — package-level deploy helpers shared by the SURVIVING deploy
// paths after the in-proc local deploy target externalized into candy/plugin-deploy-local.
// They formerly lived in the deleted in-proc local-target file (removed in this cutover):
//
//   - renderHostPackageCommand: the format's phase.install.host package-install render
//     (used by the external vm deploy AND the RunHostStep SystemPackages arm).
//   - EmitOpts.ContextOrDefault: a small shared utility.
//   - runSudoShell: the host sudo wrapper used by deploy_executor.go + reverse_ops.go.
//
// renderBuilderScript + hostBuilderContext relocated to sdk/deploykit/localpkg.go (W3,
// deploykit.RenderBuilderScript) — pure, no *Config/registry dependency; callers here
// (builder_venue.go) call the exported deploykit form directly.

import (
	"fmt"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// renderHostPackageCommand renders the host-venue package-install command for a
// SystemPackagesStep from the format's phase.install.host cell in the embedded vocabulary —
// the SAME PhaseTemplate + NewInstallContext + RenderTemplate path deploykit.OCITarget uses for the
// container venue (R3). No hardcoded dnf/apt/pacman dispatch: the format selects the
// template; the command is config-driven.
//
// Returns ("", nil) when the step is not an install-phase step, has no packages, or the
// format declares no host cell — all "nothing to run", not errors. A missing DistroConfig /
// format definition IS an error (the deploy can't honor a package step it can't render).
func renderHostPackageCommand(distroCfg *buildkit.DistroConfig, s *deploykit.SystemPackagesStep) (string, error) {
	if s.Phase != spec.PhaseInstall || len(s.Packages) == 0 {
		return "", nil
	}
	if distroCfg == nil {
		return "", fmt.Errorf("no distro config for format %q host install", s.Format)
	}
	formatDef := distroCfg.FindFormat(s.Format)
	if formatDef == nil {
		return "", fmt.Errorf("no format %q in distro config", s.Format)
	}
	tmpl := formatPhaseTemplate(formatDef, spec.PhaseInstall, spec.VenueHostNative)
	if tmpl == "" {
		return "", nil // no host cell for this format → skip
	}
	ctx := buildkit.NewInstallContext(s.RawInstallContext, formatDefCacheMountDefs(formatDef))
	cmd, err := buildkit.RenderTemplate(s.Format+"-host-install", tmpl, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s host install template: %w", s.Format, err)
	}
	return strings.TrimSpace(cmd), nil
}

// hostReverseExec is the ReverseExecutor adapter combining a teardown's gate flags with a
// per-call DryRun + ReverseRunner. Used by the host-teardown path (externalDeployTarget.Del
// for the local/external substrate). Formerly lived in deploy_target_external.go.
type hostReverseExec struct {
	DryRun          bool
	KeepRepoChanges bool
	KeepServices    bool
	Runner          kit.ReverseRunner
}

func (e *hostReverseExec) ReverseDryRun() bool              { return e.DryRun }
func (e *hostReverseExec) ReverseKeepRepoChanges() bool     { return e.KeepRepoChanges }
func (e *hostReverseExec) ReverseKeepServices() bool        { return e.KeepServices }
func (e *hostReverseExec) ReverseRunner() kit.ReverseRunner { return e.Runner }

// teardownHostDeploy reverses a single host/external deploy record: for each candy whose
// refcount drops to zero it replays the recorded ReverseOps, removes the env.d file, and
// deletes the candy record; then deletes the deploy record. Only RECORDED ops are replayed
// (record-and-replay). Shared by externalDeployTarget.Del (the local/external host-venue
// teardown). Formerly lived in deploy_target_external.go.
func teardownHostDeploy(paths *kit.LedgerPaths, rec *kit.DeployRecord, hostHome string, re kit.ReverseExecutor) error {
	for _, layer := range rec.Candy {
		candyRec, shouldRemove, err := kit.RemoveCandyDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return err
		}
		if !shouldRemove {
			continue
		}
		kit.RunReverseOps(candyRec.ReverseOps, re)
		_ = RemoveEnvdFile(hostHome, layer)
		if err := kit.DeleteCandyRecord(paths, layer); err != nil {
			return err
		}
	}
	return kit.DeleteDeployRecord(paths, rec.DeployID)
}
