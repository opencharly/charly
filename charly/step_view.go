package main

// step_view.go — the SINGLE bridge between the in-core InstallStep IR and its
// serializable wire form (spec.InstallStepView). stepToView projects a concrete step
// onto the wire union; stepFromView reconstructs the SAME concrete step from a view.
//
// This is the R3 single source for step serialization: the in-proc DeployTargets keep
// walking []InstallStep unchanged, and the external (out-of-process) deploy/step plugin
// receives []spec.InstallStepView carrying identical data. Because every step's
// Scope()/Venue()/RequiresGate()/Reverse() are METHODS computed from stored fields,
// reconstructing the concrete type yields byte-identical behaviour — the wire path and
// the in-proc path cannot diverge. The step-IR round-trip test (step_view_test.go)
// proves stepFromView(stepToView(s)) is DeepEqual to s for every kind.

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// stepToView projects one concrete InstallStep onto its serializable wire view.
func stepToView(step InstallStep) spec.InstallStepView {
	v := spec.InstallStepView{
		Kind: string(step.Kind()),
		// Derived advisory fields — computed once from the step's own methods so an
		// executing plugin doesn't recompute the scope/venue/gate rule (R3). Ignored by
		// stepFromView (the reconstructed step recomputes them), so they never affect
		// round-trip identity.
		Scope: step.Scope(),
		Venue: int(step.Venue()),
		Gate:  string(step.RequiresGate()),
		// The step's teardown ops, computed ONCE host-side (Fork A): a plugin that
		// executes a plugin-renderable step itself cannot call the package-main Reverse()
		// method, so it ECHOES these. For the deploy-time-stateful kinds the caller
		// captures the venue state (PriorEnabled / EnvFile) on the live venue BEFORE
		// projecting the view, so these ops are faithful. Advisory for the HOST-ENGINE
		// kinds (RunHostStep returns their reverse ops separately). Ignored by
		// stepFromView — never affects round-trip identity.
		ReverseOps: step.Reverse(),
	}
	switch s := step.(type) {
	case *SystemPackagesStep:
		v.Format = s.Format
		v.Phase = int(s.Phase)
		v.Packages = s.Packages
		v.Repos = reposToView(s.Repos)
		v.Options = s.Options
		v.Copr = s.Copr
		v.Modules = s.Modules
		v.Exclude = s.Exclude
		v.Keys = s.Keys
		v.CacheMount = cacheMountsToView(s.CacheMount)
		v.RawInstallContext = s.RawInstallContext
	case *BuilderStep:
		v.Builder = s.Builder
		v.BuilderImage = s.BuilderImage
		v.CandyName = s.CandyName
		v.CandyDir = s.CandyDir
		v.Phase = int(s.Phase)
		v.Artifacts = artifactsToView(s.Artifacts)
		v.RawStageContext = s.RawStageContext
		v.LocalPkg = s.LocalPkg
		v.BuilderDef = s.BuilderDef
	case *OpStep:
		v.Op = s.Op
		v.CandyName = s.CandyName
		v.CandyDir = s.CandyDir
		v.CtxPath = s.CtxPath
		v.ResolvedUser = s.ResolvedUser
		v.To = s.To
		v.CandyVars = s.CandyVars
		v.Distros = s.Distros
	case *FileStep:
		v.Source = s.Source
		v.Dest = s.Dest
		v.Mode = uint32(s.Mode)
		v.Owner = s.Owner
		v.CandyName = s.CandyName
	case *ServicePackagedStep:
		v.Unit = s.Unit
		v.TargetScope = s.TargetScope
		v.Enable = s.Enable
		v.OverridesText = s.OverridesText
		v.OverridesPath = s.OverridesPath
		v.CandyName = s.CandyName
		v.PriorEnabled = s.PriorEnabled
	case *ServiceCustomStep:
		v.Name = s.Name
		v.UnitText = s.UnitText
		v.UnitPath = s.UnitPath
		v.TargetScope = s.TargetScope
		v.Enable = s.Enable
		v.CandyName = s.CandyName
	case *ShellHookStep:
		v.CandyName = s.CandyName
		v.EnvVars = s.EnvVars
		v.PathAdd = s.PathAdd
		v.EnvFile = s.EnvFile
	case *ShellSnippetStep:
		v.CandyName = s.CandyName
		v.Origin = s.Origin
		v.Shell = s.Shell
		v.Snippet = s.Snippet
		v.PathAppend = s.PathAppend
		v.Destination = s.Destination
		v.Marker = s.Marker
		v.UseDropin = s.UseDropin
		v.Priority = s.Priority
	case *RepoChangeStep:
		v.Format = s.Format
		v.File = s.File
		v.Content = s.Content
		v.Checksum = s.Checksum
		v.CandyName = s.CandyName
	case *ApkInstallStep:
		v.ApkPackages = s.Packages
		v.CandyName = s.CandyName
		v.CandyDir = s.CandyDir
	case *LocalPkgInstallStep:
		v.PkgbuildRef = s.PkgbuildRef
		v.CandyName = s.CandyName
		v.CandyDir = s.CandyDir
		v.ProjectDir = s.ProjectDir
		v.Format = s.Format
		v.LocalPkg = s.LocalPkg
	case *RebootStep:
		v.CandyName = s.CandyName
	case *ExternalPluginStep:
		v.Op = s.Op
		v.CandyName = s.CandyName
		v.ResolvedUser = s.ResolvedUser
		v.Distros = s.Distros
	case *externalStep:
		// EXTERNAL (plugin-contributed) kind: the opaque Payload is the only typed data; the
		// Scope/Venue/Gate set above are NOT advisory here — they are the plugin-DECLARED
		// contract stepFromView reconstructs from (the authoritative flip). ReverseOps set
		// above carry the dynamically-recorded teardown ops.
		v.Payload = s.Payload
	}
	return v
}

// stepFromView reconstructs the concrete InstallStep a view projects. An unknown Kind
// is a hard error (the wire carried a step kind this charly does not know — a genuine
// version/contract breach, never silently dropped).
func stepFromView(v spec.InstallStepView) (InstallStep, error) {
	switch StepKind(v.Kind) {
	case StepKindSystemPackages:
		return &SystemPackagesStep{
			Format:            v.Format,
			Phase:             Phase(v.Phase),
			Packages:          v.Packages,
			Repos:             reposFromView(v.Repos),
			Options:           v.Options,
			Copr:              v.Copr,
			Modules:           v.Modules,
			Exclude:           v.Exclude,
			Keys:              v.Keys,
			CacheMount:        cacheMountsFromView(v.CacheMount),
			RawInstallContext: v.RawInstallContext,
		}, nil
	case StepKindBuilder:
		return &BuilderStep{
			Builder:         v.Builder,
			BuilderImage:    v.BuilderImage,
			CandyName:       v.CandyName,
			CandyDir:        v.CandyDir,
			Phase:           Phase(v.Phase),
			Artifacts:       artifactsFromView(v.Artifacts),
			RawStageContext: v.RawStageContext,
			LocalPkg:        v.LocalPkg,
			BuilderDef:      v.BuilderDef,
			// A BuilderStep's teardown ops are PRE-RESOLVED host-side (builder_preresolve.go, the
			// externalized builder plugin's OpReverse), so — unlike every other kind, whose ReverseOps
			// view field is advisory and ignored here — the reconstructed BuilderStep MUST restore them:
			// RunHostStep recomputes st.Reverse() host-side (now a pure getter returning this slice), and
			// the builder plugin may not be reachable from that nested reverse leg. Identity-preserving:
			// stepToView set v.ReverseOps = s.Reverse() = s.PreResolvedReverse.
			PreResolvedReverse: v.ReverseOps,
		}, nil
	case StepKindOp:
		return &OpStep{
			Op:           v.Op,
			CandyName:    v.CandyName,
			CandyDir:     v.CandyDir,
			CtxPath:      v.CtxPath,
			ResolvedUser: v.ResolvedUser,
			To:           v.To,
			CandyVars:    v.CandyVars,
			Distros:      v.Distros,
		}, nil
	case StepKindFile:
		return &FileStep{
			Source:    v.Source,
			Dest:      v.Dest,
			Mode:      os.FileMode(v.Mode),
			Owner:     v.Owner,
			CandyName: v.CandyName,
		}, nil
	case StepKindServicePackaged:
		return &ServicePackagedStep{
			Unit:          v.Unit,
			TargetScope:   v.TargetScope,
			Enable:        v.Enable,
			OverridesText: v.OverridesText,
			OverridesPath: v.OverridesPath,
			CandyName:     v.CandyName,
			PriorEnabled:  v.PriorEnabled,
		}, nil
	case StepKindServiceCustom:
		return &ServiceCustomStep{
			Name:        v.Name,
			UnitText:    v.UnitText,
			UnitPath:    v.UnitPath,
			TargetScope: v.TargetScope,
			Enable:      v.Enable,
			CandyName:   v.CandyName,
		}, nil
	case StepKindShellHook:
		return &ShellHookStep{
			CandyName: v.CandyName,
			EnvVars:   v.EnvVars,
			PathAdd:   v.PathAdd,
			EnvFile:   v.EnvFile,
		}, nil
	case StepKindShellSnippet:
		return &ShellSnippetStep{
			CandyName:   v.CandyName,
			Origin:      v.Origin,
			Shell:       v.Shell,
			Snippet:     v.Snippet,
			PathAppend:  v.PathAppend,
			Destination: v.Destination,
			Marker:      v.Marker,
			UseDropin:   v.UseDropin,
			Priority:    v.Priority,
		}, nil
	case StepKindRepoChange:
		return &RepoChangeStep{
			Format:    v.Format,
			File:      v.File,
			Content:   v.Content,
			Checksum:  v.Checksum,
			CandyName: v.CandyName,
		}, nil
	case StepKindApkInstall:
		return &ApkInstallStep{
			Packages:  v.ApkPackages,
			CandyName: v.CandyName,
			CandyDir:  v.CandyDir,
		}, nil
	case StepKindLocalPkgInstall:
		return &LocalPkgInstallStep{
			PkgbuildRef: v.PkgbuildRef,
			CandyName:   v.CandyName,
			CandyDir:    v.CandyDir,
			ProjectDir:  v.ProjectDir,
			Format:      v.Format,
			LocalPkg:    v.LocalPkg,
		}, nil
	case StepKindReboot:
		return &RebootStep{CandyName: v.CandyName}, nil
	case StepKindExternalPlugin:
		return &ExternalPluginStep{
			Op:           v.Op,
			CandyName:    v.CandyName,
			ResolvedUser: v.ResolvedUser,
			Distros:      v.Distros,
		}, nil
	}
	// EXTERNAL (plugin-contributed) step kind (F3): "external:<word>" — no compiled-in case.
	// Rebuild from the AUTHORITATIVE carried Scope/Venue/Gate (the plugin-DECLARED contract,
	// which the host cannot recompute, there being no concrete Go step) + the opaque Payload.
	// This is the advisory→authoritative flip: for builtin kinds Scope/Venue/Gate are ignored
	// here (recomputed by the concrete step), for an external kind they ARE the contract.
	if isExternalStepKind(StepKind(v.Kind)) {
		return &externalStep{
			Word:      strings.TrimPrefix(v.Kind, externalStepKindPrefix),
			ScopeV:    v.Scope,
			VenueV:    Venue(v.Venue),
			GateV:     Gate(v.Gate),
			Payload:   v.Payload,
			CandyName: v.CandyName,
		}, nil
	}
	return nil, fmt.Errorf("stepFromView: unknown step kind %q", v.Kind)
}

// stepsToView / stepsFromView convert a whole ordered slice. Order is preserved
// (the InstallPlan's step ordering is load-bearing — see ResolveHome / StepsByVenue).
func stepsToView(steps []InstallStep) []spec.InstallStepView {
	if len(steps) == 0 {
		return nil
	}
	out := make([]spec.InstallStepView, 0, len(steps))
	for _, s := range steps {
		out = append(out, stepToView(s))
	}
	return out
}

func stepsFromView(views []spec.InstallStepView) ([]InstallStep, error) {
	if len(views) == 0 {
		return nil, nil
	}
	out := make([]InstallStep, 0, len(views))
	for _, v := range views {
		s, err := stepFromView(v)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Small package-main↔spec mirror helpers.
// ---------------------------------------------------------------------------

func reposToView(repos []RepoSpec) []map[string]any {
	if len(repos) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Raw)
	}
	return out
}

func reposFromView(repos []map[string]any) []RepoSpec {
	if len(repos) == 0 {
		return nil
	}
	out := make([]RepoSpec, 0, len(repos))
	for _, r := range repos {
		out = append(out, RepoSpec{Raw: r})
	}
	return out
}

func cacheMountsToView(mounts []CacheMountSpec) []spec.CacheMountView {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]spec.CacheMountView, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, spec.CacheMountView{Dst: m.Dst, Sharing: m.Sharing})
	}
	return out
}

func cacheMountsFromView(mounts []spec.CacheMountView) []CacheMountSpec {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]CacheMountSpec, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, CacheMountSpec{Dst: m.Dst, Sharing: m.Sharing})
	}
	return out
}

func artifactsToView(arts []ArtifactRef) []spec.ArtifactView {
	if len(arts) == 0 {
		return nil
	}
	out := make([]spec.ArtifactView, 0, len(arts))
	for _, a := range arts {
		out = append(out, spec.ArtifactView{ContainerPath: a.ContainerPath, HostPath: a.HostPath, Chown: a.Chown})
	}
	return out
}

func artifactsFromView(arts []spec.ArtifactView) []ArtifactRef {
	if len(arts) == 0 {
		return nil
	}
	out := make([]ArtifactRef, 0, len(arts))
	for _, a := range arts {
		out = append(out, ArtifactRef{ContainerPath: a.ContainerPath, HostPath: a.HostPath, Chown: a.Chown})
	}
	return out
}
