package main

// deploy_host_helpers.go — package-level deploy helpers shared by the SURVIVING deploy
// paths after the in-proc local deploy target externalized into candy/plugin-deploy-local.
// They formerly lived in the deleted in-proc local-target file (removed in this cutover):
//
//   - renderHostPackageCommand: the format's phase.install.host package-install render
//     (used by the external vm deploy AND the RunHostStep SystemPackages arm).
//   - renderBuilderScript + hostBuilderContext: the builder phase.install.host render
//     (used by the host-engine builder leg: RunHostStep → runVenueBuilderStep).
//   - EmitOpts.ContextOrDefault: a small shared utility.
//   - runSudoShell: the host sudo wrapper used by deploy_executor.go + reverse_ops.go.

import (
	"fmt"
	"strings"
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
func renderHostPackageCommand(distroCfg *DistroConfig, s *SystemPackagesStep) (string, error) {
	if s.Phase != PhaseInstall || len(s.Packages) == 0 {
		return "", nil
	}
	if distroCfg == nil {
		return "", fmt.Errorf("no distro config for format %q host install", s.Format)
	}
	formatDef := distroCfg.FindFormat(s.Format)
	if formatDef == nil {
		return "", fmt.Errorf("no format %q in distro config", s.Format)
	}
	tmpl := formatPhaseTemplate(formatDef, PhaseInstall, VenueHostNative)
	if tmpl == "" {
		return "", nil // no host cell for this format → skip
	}
	ctx := NewInstallContext(s.RawInstallContext, formatDefCacheMountDefs(formatDef))
	cmd, err := RenderTemplate(s.Format+"-host-install", tmpl, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s host install template: %w", s.Format, err)
	}
	return strings.TrimSpace(cmd), nil
}

// hostBuilderContext is the template context for a builder's phase.install.host cell. The
// HOME/PIXI_CACHE_DIR/NPM_CONFIG_PREFIX/CARGO_HOME values are injected by BuilderRunOpts.Env
// (the cells read them as $HOME/$CARGO_HOME), so the only template-visible datum is the
// package list (consumed by the aur cell).
type hostBuilderContext struct {
	HostHome string
	Packages []string
}

// renderBuilderScript turns a BuilderStep into the bash script that runs inside the builder
// container — the host-side (deploy) analog of the build-time multi-stage, fully config-driven:
// it renders the builder's phase.install.host cell via the SAME RenderTemplate engine
// (text/template). HOME/PIXI_CACHE_DIR/NPM_CONFIG_PREFIX/CARGO_HOME are injected by
// BuilderRunOpts.Env before the script starts.
func renderBuilderScript(s *BuilderStep, hostHome string) (string, error) {
	if s.BuilderDef == nil {
		return "", fmt.Errorf("builder %q: no builder definition (BuilderDef unset)", s.Builder)
	}
	tmpl := builderPhaseTemplate(s.BuilderDef, PhaseInstall, VenueHostNative)
	if tmpl == "" {
		return "", fmt.Errorf("builder %q: no phase.install.host template in the embedded build vocabulary", s.Builder)
	}
	ctx := hostBuilderContext{
		HostHome: hostHome,
		Packages: extractStringSlice(s.RawStageContext, "packages"),
	}
	script, err := RenderTemplate(s.Builder+"-host", tmpl, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s host builder template: %w", s.Builder, err)
	}
	return script, nil
}

// hostReverseExec is the ReverseExecutor adapter combining a teardown's gate flags with a
// per-call DryRun + ReverseRunner. Used by the host-teardown path (externalDeployTarget.Del
// for the local/external substrate). Formerly lived in deploy_target_external.go.
type hostReverseExec struct {
	DryRun          bool
	KeepRepoChanges bool
	KeepServices    bool
	Runner          ReverseRunner
}

func (e *hostReverseExec) ReverseDryRun() bool          { return e.DryRun }
func (e *hostReverseExec) ReverseKeepRepoChanges() bool { return e.KeepRepoChanges }
func (e *hostReverseExec) ReverseKeepServices() bool    { return e.KeepServices }
func (e *hostReverseExec) ReverseRunner() ReverseRunner { return e.Runner }

// teardownHostDeploy reverses a single host/external deploy record: for each candy whose
// refcount drops to zero it replays the recorded ReverseOps, removes the env.d file, and
// deletes the candy record; then deletes the deploy record. Only RECORDED ops are replayed
// (record-and-replay). Shared by externalDeployTarget.Del (the local/external host-venue
// teardown). Formerly lived in deploy_target_external.go.
func teardownHostDeploy(paths *LedgerPaths, rec *DeployRecord, hostHome string, re ReverseExecutor) error {
	for _, layer := range rec.Candy {
		candyRec, shouldRemove, err := RemoveCandyDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return err
		}
		if !shouldRemove {
			continue
		}
		runReverseOps(candyRec.ReverseOps, re)
		_ = RemoveEnvdFile(hostHome, layer)
		if err := DeleteCandyRecord(paths, layer); err != nil {
			return err
		}
	}
	return DeleteDeployRecord(paths, rec.DeployID)
}
