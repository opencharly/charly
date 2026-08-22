package deploypod

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/shellquote"
	"github.com/opencharly/spec/spec"
)

// overlay.go — the pod-overlay BUILD RENDER, DISSOLVED out of charly core into the candy (P11c).
// The pod-only render body — the former PodDeployTarget.buildOverlay / renderOverlayServices /
// renderOverlaySecurityLabel / tagDeployAlias / collectOverlayCandies / filterPlansByCandies /
// overlayTagFor / translateHostPathToVenue (charly/deploy_target_pod.go) — MOVES HERE, byte-for-
// byte. The candy imports sdk/spec + sdk/deploykit + sdk/buildkit + sdk/kit DIRECTLY (IMPORT-
// PURITY — NO `OCITarget = deploykit.OCITarget` alias, ZERO-ALIASES), constructs the
// deploykit.Generator itself (via the shared deploykit.NewRenderGeneratorFromProject helper — the
// SAME source candy/plugin-build uses, R3/DRY), renders the overlay Containerfile in its own code,
// and runs podman build + the deploy-name alias tag via the served host executor. Each per-step
// Containerfile fragment is rendered HOST-SIDE over the generic "step-emit" host-builder: the
// deploykit.OCITarget.EmitStepOp seam calls HostBuild("step-emit", {Word:"oci-emit-step",
// Payload: OCIEmitStepParams{Dir, StepView, PlanView}, Distros}) per step; the host reconstructs
// the step + plan from their wire views + calls dispatchOCIStep (the SAME single source of truth the
// former in-core OCITarget.emitStep delegated to — byte-identical fragments).
//
// The host prep (charly/build_overlay.go hostBuildOverlay) returns the OverlayBuildReply envelope
// (ResolvedProject + Plans + base-image metadata + per-overlay-candy security + the parent
// bind-mount volumes for a nested pod-in-pod). The candy consumes it here. The render is
// byte-faithful to the former in-core PodDeployTarget: the SAME Containerfile structure (FROM
// scratch stages → FROM base → USER root → OCITarget fragment → service append → security LABEL
// → USER restore), the SAME podman build + tag scripts (shellquote.ShellQuote == the core shellSingleQuote
// / deployShellQuote, byte-identical), the SAME overlay tag hash. The orchestrator's
// `charly check run check-pod` bed is the parity gate (R8).

// engineBin is the container engine the overlay build + alias tag use. The overlay image is
// always built with podman (mirrors the former PodDeployTarget.Emit which defaulted t.Engine to
// "podman" — charly/runOverlayBuild never threaded the deploy node's engine onto the overlay
// build). The container itself may run under a different engine (docker), but the image
// synthesis + the deploy-name alias tag use podman.
const engineBin = "podman"

// buildOverlay synthesizes the overlay Containerfile in the candy + builds the image + tags the
// deploy-name alias. The EX-`PodDeployTarget.buildOverlay` body, byte-faithful, adapted to consume
// the prep envelope (reply, now HOST-ONLY metadata) + a SEPARATE render-prepped rp
// (*spec.ResolvedProject — fetched by the caller directly via
// InvokeProvider("build","generate",sdk.OpResolve,…), #55 step3 3-II: the host prep no longer
// projects this envelope itself) + the ALREADY-DECODED live plans (the caller decodes reply.Plans
// once — to compute the ExtraCandyRefs the resolve call needs — and passes them here rather than
// this function re-decoding the same InstallPlanViews a second time, R3). dir is the project dir
// (the build-context root); baseName is the base box name (req.Image / DeployName — the key into
// rp.Boxes); opts carries DryRun + the alias-tag gates. baseUser/baseSecurity/baseRegistry are the
// base image's OCI-label metadata (K3-W2: read PLUGIN-SIDE by the caller via deploykit.ExtractMetadata
// — no longer part of the OverlayBuildReply wire type).
func buildOverlay(ctx context.Context, exec *sdk.Executor, reply spec.OverlayBuildReply, rp *spec.ResolvedProject, plans []*spec.InstallPlan, dir, baseName string, opts spec.LifecycleOpts, baseUser string, baseSecurity *spec.Security, baseRegistry string) (string, error) {
	// Construct the deploykit.Generator from the resolved-project envelope — the SAME shared
	// construction source candy/plugin-build uses (R3/DRY). Its seams (RenderService for service
	// fragments, the builder resolves, egress) are InvokeProvider PEER-DISPATCH — since K-wave 2
	// cone R1 none of them makes a HostBuild callback (placement-invisible either way).
	dg, err := deploykit.NewRenderGeneratorFromProject(ctx, exec, rp, dir, false, nil)
	if err != nil {
		return "", fmt.Errorf("overlay render: %w", err)
	}

	// Resolve the overlay candies' secret_requires:/secret_accepts: env PLUGIN-SIDE + inject it
	// into the plans' TaskSteps before oci.Emit walks them (#55 coneB-br2 — the α cluster
	// relocated host-side from charly/enc.go + charly/layer_secrets.go + charly/build_overlay.go,
	// which are DELETED in that cutover). This mirrors candy/plugin-fleet/secrets_artifacts.go's
	// injectCandySecrets exactly: the candy set is dg.Candies (the resolved-project envelope's
	// candy models — deploykit.CandyModel is a spec.CandyReader alias, so it passes directly to
	// SelectCandiesForPlans); the credential access is the shared
	// deploykit.CredentialAccessViaExecutor (verb:credential = candy/plugin-secrets over the
	// reverse channel — the SAME store the former host coreCredentialAccess reached via
	// ResolveCredential/DefaultCredentialStore). Byte-identical to the former host-side
	// resolveCandySecrets → deploykit.ResolveSecretForC candy → deploykit.InjectSecretsIntoPlans.
	candyList := deploykit.SelectCandiesForPlans(plans, dg.Candies)
	secretEnv := deploykit.ResolveSecretForCandy(candyList, deploykit.CredentialAccessViaExecutor(ctx, exec))
	deploykit.InjectSecretsIntoPlans(plans, secretEnv)

	overlayCandies := collectOverlayCandies(plans)

	// The overlay build dir (relative to the project root = the build-context root). The emitted
	// Containerfile + the service fragments live here; the per-candy FROM scratch COPY sources +
	// the inline-content staging live under the project root (.build/_candy / .build/_inline).
	overlayDir := filepath.Join(".build", "overlay-"+reply.DeployName)
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		return "", fmt.Errorf("overlay build dir: %w", err)
	}

	// createRemoteCandyCopies (staging remote add_candy source trees under .build/_candy/) ran
	// HOST-SIDE in the prep (host-fs materialization a sdk-only candy cannot do) — the candy's
	// FROM scratch COPY references those staged paths via dg.CandyCopySource.

	// The base box — the candy reads its Home/Tags (for the OCITarget walker) + Candy/InitSystem/
	// InitDef (for renderOverlayServices) from the envelope (re-attached by NewSpecResolvedBox).
	box := dg.Boxes[baseName]

	// The overlay OCITarget walker. EmitStepOp is the per-step render seam: each step's fragment is
	// rendered HOST-SIDE via HostBuild("step-emit", "oci-emit-step", …) (the host looks up the
	// cached overlay buildEngineContext by Dir + calls dispatchOCIStep — the full provider-registry
	// dispatch, byte-identical to the former in-core OCITarget.emitStep). Home/Distros feed the
	// walker's ResolveHome + per-step distro threading (the former OCITarget.read them off t.Box).
	var home string
	var distros []string
	if box != nil {
		home = box.Home
		distros = box.Tags
	}
	oci := &deploykit.OCITarget{
		Dir:     dir,
		Home:    home,
		Distros: distros,
		EmitStepOp: func(step spec.InstallStep, plan *spec.InstallPlan, stepDistros []string) (string, error) {
			params := spec.OCIEmitStepParams{
				Dir:      dir,
				StepView: deploykit.StepToView(step),
				PlanView: deploykit.WireView(plan),
			}
			payload, merr := json.Marshal(params)
			if merr != nil {
				return "", fmt.Errorf("oci-emit-step: marshal params: %w", merr)
			}
			reqJSON, merr := json.Marshal(spec.StepEmitRequest{Word: "oci-emit-step", Payload: payload, Distros: stepDistros})
			if merr != nil {
				return "", fmt.Errorf("oci-emit-step: marshal request: %w", merr)
			}
			resJSON, herr := exec.HostBuild(ctx, "step-emit", reqJSON)
			if herr != nil {
				return "", fmt.Errorf("oci-emit-step: %w", herr)
			}
			var er spec.EmitReply
			if uerr := json.Unmarshal(resJSON, &er); uerr != nil {
				return "", fmt.Errorf("oci-emit-step: decode reply: %w", uerr)
			}
			return er.Fragment, nil
		},
	}

	// Render the overlay candies' task fragments (RUN/COPY/…). Only the overlay candies' plans —
	// the base image's candies are already baked. deploykit.OCITarget.Emit walks each plan's steps
	// + delegates to EmitStepOp per step (the host render).
	filtered := filterPlansByCandies(plans, overlayCandies)
	if err := oci.Emit(filtered, spec.EmitOpts{}); err != nil {
		return "", err
	}

	var cf bytes.Buffer
	fmt.Fprintf(&cf, "# Overlay Containerfile for deploy %q\n", reply.DeployName)
	fmt.Fprintf(&cf, "# Extra candies: %s\n\n", strings.Join(overlayCandies, ", "))
	// Per-candy FROM scratch context stages, emitted BEFORE the main FROM. The tasks emitted by
	// oci.Emit reference these via `--mount=type=bind,from=<layer>` (same as the full build). The
	// COPY source is keyed by the candy's store key (CandyMapKey) — a remote add_candy candy's
	// source lives at .build/_candy/<name>.<version>, reachable only via the qualified key.
	for _, candyName := range overlayCandies {
		layer := candyByName(dg.Candies, candyName)
		if layer == nil {
			continue
		}
		fmt.Fprintf(&cf, "FROM scratch AS %s\n", layer.GetName())
		fmt.Fprintf(&cf, "COPY %s/ /\n\n", dg.CandyCopySource(deploykit.CandyMapKey(layer)))
	}
	// Service scratch stage (from the overlay candies' service: blocks) — a scratch stage holding
	// the rendered init fragments + a RUN-append line inside the main stage. Uses the cached
	// Generator's init-fragment pipeline (GenerateInitFragments → RenderService, peer-dispatched to
	// candy/plugin-init), the SAME path as the full image build.
	var svcStage, svcAppend string
	if box != nil {
		var svcErr error
		svcStage, svcAppend, svcErr = renderOverlayServices(dg, box, overlayCandies, reply.DeployName)
		if svcErr != nil {
			return "", svcErr
		}
	}
	// The service scratch stage must come BEFORE the main FROM so buildah sees it when the
	// main-stage RUN does `--mount=type=bind,from=<stage>`.
	if svcStage != "" {
		cf.WriteString(svcStage)
	}
	fmt.Fprintf(&cf, "FROM %s\n\n", reply.BaseImage)
	// Reset to USER root after FROM so candy tasks with `user: root` (most install/config tasks)
	// run with the correct privileges (the full-build convention).
	cf.WriteString("USER root\n\n")
	cf.WriteString(oci.String())
	// Append service fragments inside the MAIN image stage (after all candy tasks). This extends
	// the base image's /etc/supervisord.conf instead of replacing it.
	if svcAppend != "" {
		cf.WriteString(svcAppend)
	}
	// Merge overlay-candy security into the base image's LabelSecurity + re-emit so `charly config`
	// (quadlet generator) picks up intrinsic requirements declared by add_candy.
	if label := renderOverlaySecurityLabel(dg, reply, overlayCandies, baseSecurity); label != "" {
		cf.WriteString(label)
	}
	// Restore the base image's USER directive. The overlay set USER root above so package installs
	// work; without restoration, USER=root leaks into the resulting image + breaks every downstream
	// invariant that depends on the base running as a non-root user.
	if baseUser != "" && baseUser != "root" {
		fmt.Fprintf(&cf, "\nUSER %s\n", baseUser)
	}

	cfPath := filepath.Join(overlayDir, "Containerfile")
	if err := os.WriteFile(cfPath, cf.Bytes(), 0644); err != nil {
		return "", err
	}

	// Deterministic overlay tag: hash of base + sorted candy set. Same inputs → same tag, so
	// re-deploys of the same config don't churn overlay images.
	tag := overlayTagFor(reply.BaseImage, overlayCandies)
	overlayRef := fmt.Sprintf("%s-overlay:%s", reply.DeployName, tag)

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] %s build -f %s -t %s %s\n", engineBin, cfPath, overlayRef, overlayDir)
		return overlayRef, nil
	}

	// Build context is the PROJECT ROOT (dg.Dir), not the overlay build dir — the emitted
	// Containerfile has `COPY candy/<name>/ /` paths relative to the project root, same as the
	// full build (candyCopySource). For a nested pod-in-pod overlay, translate host-side paths to
	// venue-side paths via the parent's bind-mount volumes (the candy's served executor IS the
	// parent venue executor for a nested overlay).
	buildContext := dg.Dir
	cfPathInVenue := cfPath
	venueBuildContext := buildContext
	if len(reply.ParentVolumes) > 0 {
		venuePath, ok := translateHostPathToVenue(buildContext, reply.ParentVolumes)
		if !ok {
			return "", fmt.Errorf("plugin-deploy-pod: nested container overlay build requires the project tree at %s to be bind-mounted into the parent venue (set `volumes: [{name: project, type: bind, host: %s, path: /workspace}]` on the parent charly.yml entry, then re-run)", buildContext, buildContext)
		}
		venueBuildContext = venuePath
		if cfVenue, ok := translateHostPathToVenue(cfPath, reply.ParentVolumes); ok {
			cfPathInVenue = cfVenue
		}
	}

	buildScript := fmt.Sprintf("%s build -f %s -t %s %s",
		engineBin, shellquote.ShellQuote(cfPathInVenue), shellquote.ShellQuote(overlayRef), shellquote.ShellQuote(venueBuildContext))
	if err := exec.VenueRunSilent(ctx, buildScript); err != nil {
		return "", fmt.Errorf("overlay build: %w", err)
	}

	// Tag the overlay under <registry>/<deploy-name>:<calver> so deployment-name-keyed commands
	// (`charly config`, `charly start`) resolve it when deploy-name != image-name.
	if err := tagDeployAlias(ctx, exec, reply, overlayRef, opts, baseRegistry); err != nil {
		return "", err
	}
	return overlayRef, nil
}

// renderOverlayServices hooks into the deploykit.Generator init-fragment pipeline to render
// service: blocks from overlay candies into fragment files, emit a scratch stage holding them, +
// emit a RUN step that APPENDS the rendered fragments to the base image's existing
// /etc/supervisord.conf. The EX-`PodDeployTarget.renderOverlayServices` body, byte-faithful,
// adapted to read the OVERLAY-RESOLVED init (box.InitSystem/box.InitDef — the host prep resolved it
// via InitConfig.ResolveInitSystem, core-only, + carried it in the envelope) instead of calling
// ResolveInitSystem itself. Uses dg.GenerateInitFragments (sdk) for the fragment render (which
// peer-dispatches RenderService to candy/plugin-init) + candyByName(dg.Candies, n).HasInit(initName) for the
// per-candy init gate. Returns (scratchStageBlock, runAppendBlock, error).
func renderOverlayServices(dg *deploykit.Generator, box *buildkit.ResolvedBox, overlayCandies []string, deployName string) (string, string, error) {
	if dg == nil || box == nil {
		return "", "", nil
	}
	initName := box.InitSystem
	initDef := box.InitDef
	if initDef == nil || initDef.ServiceSchema == nil {
		return "", "", nil
	}
	var anySvc bool
	for _, n := range overlayCandies {
		l := candyByName(dg.Candies, n)
		if l != nil && l.HasInit(initName) {
			anySvc = true
			break
		}
	}
	if !anySvc {
		return "", "", nil
	}
	overlayImageName := "overlay-" + deployName
	// Point the Generator at the overlay build dir so GenerateInitFragments writes fragments
	// there. The overlay dir is relative to the project dir (the build-context root), so the
	// Containerfile can COPY from that path directly — no abs/rel gymnastics needed.
	overlayDir := filepath.Join(".build", "overlay-"+deployName)
	savedBuildDir := dg.BuildDir
	dg.BuildDir = overlayDir
	defer func() { dg.BuildDir = savedBuildDir }()
	if err := dg.GenerateInitFragments(overlayImageName, initName, initDef, overlayCandies); err != nil {
		return "", "", fmt.Errorf("overlay service fragments: %w", err)
	}

	var stage strings.Builder
	stageName := initDef.StageName + "-overlay"
	fmt.Fprintf(&stage, "FROM scratch AS %s\n", stageName)
	for i, candyName := range overlayCandies {
		l := candyByName(dg.Candies, candyName)
		if l == nil || !l.HasInit(initName) {
			continue
		}
		// Short name, not the slashed remote map key.
		fileName := fmt.Sprintf("%02d-%s.conf", i+1, l.GetName())
		srcRel := filepath.Join(overlayDir, overlayImageName, initDef.FragmentDir, fileName)
		fmt.Fprintf(&stage, "COPY %s /supervisor-overlay/%s\n", srcRel, fileName)
	}
	stage.WriteString("\n")

	var run strings.Builder
	run.WriteString("\n# Append overlay service fragments to base /etc/supervisord.conf\n")
	fmt.Fprintf(&run, "RUN --mount=type=bind,from=%s,source=/supervisor-overlay,target=/supervisor-overlay \\\n", stageName)
	run.WriteString("    sh -c 'for f in /supervisor-overlay/*.conf; do echo; cat \"$f\"; done >> /etc/supervisord.conf'\n")
	return stage.String(), run.String(), nil
}

// renderOverlaySecurityLabel merges the base image's baked LabelSecurity (baseSecurity, read
// plugin-side by the caller via deploykit.ExtractMetadata — K3-W2) with each overlay candy's own
// `security:` block + returns a Containerfile LABEL directive that overwrites the base's label — or
// "" if no merge is needed. #55 step3 3-II: per-candy security is no longer host-computed
// (reply.OverlayCandySecurity is GONE) — spec.CandyModel.Security() is a wire-carried field on the
// SAME render-prepped envelope dg.Candies already comes from, so the candy computes it itself via
// candyByName(dg.Candies, name).Security() (Q1, RDD-confirmed: no host-only dependency here). The
// EX-`PodDeployTarget.renderOverlaySecurityLabel` body otherwise byte-faithful: same merge semantics
// (Privileged OR, CgroupNS last-writer, CapAdd/Devices/SecurityOpt/GroupAdd/Mounts appendUnique
// dedup), same json.Marshal, same LABEL directive (shellquote.ShellQuote == the core shellSingleQuote,
// byte-identical). Picked up at deploy time by `charly config` via ExtractMetadata.
func renderOverlaySecurityLabel(dg *deploykit.Generator, reply spec.OverlayBuildReply, overlayCandies []string, baseSecurity *spec.Security) string {
	if reply.BaseImage == "" {
		return ""
	}
	// Start from the base image's existing security (a COPY — Security is a value type).
	var sec spec.Security
	if baseSecurity != nil {
		sec = *baseSecurity
	}
	added := false
	for _, candyName := range overlayCandies {
		layer := candyByName(dg.Candies, candyName)
		if layer == nil {
			continue
		}
		ls := layer.Security()
		if ls == nil {
			continue
		}
		added = true
		if ls.Privileged {
			sec.Privileged = true
		}
		if ls.CgroupNS != "" {
			sec.CgroupNS = ls.CgroupNS
		}
		sec.CapAdd = appendUnique(sec.CapAdd, ls.CapAdd...)
		sec.Devices = appendUnique(sec.Devices, ls.Devices...)
		sec.SecurityOpt = appendUnique(sec.SecurityOpt, ls.SecurityOpt...)
		sec.GroupAdd = appendUnique(sec.GroupAdd, ls.GroupAdd...)
		sec.Mounts = appendUnique(sec.Mounts, ls.Mounts...)
	}
	if !added {
		return ""
	}
	data, err := json.Marshal(sec)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("LABEL %s=%s\n", spec.LabelSecurity, shellquote.ShellQuote(string(data)))
}

// tagDeployAlias tags imageRef under <registry>/<deploy-name>:<calver> so deployment-name-keyed
// commands (`charly config setup`, `charly start`) resolve the image correctly when deploy-name
// differs from image-name. The EX-`PodDeployTarget.tagDeployAlias` body, byte-faithful, adapted to
// read the registry (baseRegistry — K3-W2: read plugin-side by the caller via
// deploykit.ExtractMetadata, no longer part of the envelope) + the CalVer (reply.CalVer — the host
// prep computed it; ComputeCalVer is host-only) from the envelope instead of running podman
// inspect / ComputeCalVer itself. CalVer-only — no `:latest` alias.
func tagDeployAlias(ctx context.Context, exec *sdk.Executor, reply spec.OverlayBuildReply, imageRef string, opts spec.LifecycleOpts, baseRegistry string) error {
	aliasRef := reply.DeployName + ":" + reply.CalVer
	if baseRegistry != "" {
		aliasRef = baseRegistry + "/" + reply.DeployName + ":" + reply.CalVer
	}
	if aliasRef == imageRef {
		return nil
	}
	tagScript := fmt.Sprintf("%s tag %s %s", engineBin, shellquote.ShellQuote(imageRef), shellquote.ShellQuote(aliasRef))
	if err := exec.VenueRunSilent(ctx, tagScript); err != nil {
		return fmt.Errorf("deploy-name alias tag: %w", err)
	}
	return nil
}

// collectOverlayCandies returns the set of candy names declared as add_candy in any plan's meta.
// The EX-`PodDeployTarget`-adjacent helper (charly/deploy_target_pod.go), byte-faithful: union all
// plans' AddCandies slices. Pure (no core state); the candy keeps its own copy because it cannot
// import charly core, + the host prep keeps its own copy for the prep (R3 — cross-module reuse is
// fine; the two modules cannot import each other).
func collectOverlayCandies(plans []*spec.InstallPlan) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range plans {
		for _, n := range p.AddCandies {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// filterPlansByCandies returns only the plans whose Candy is in names. The EX-core helper,
// byte-faithful. Pure.
func filterPlansByCandies(plans []*spec.InstallPlan, names []string) []*spec.InstallPlan {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []*spec.InstallPlan
	for _, p := range plans {
		if want[p.Candy] {
			out = append(out, p)
		}
	}
	return out
}

// overlayTagFor computes a deterministic short tag from the base image ref + the (sorted) overlay
// candy set. The EX-core helper, byte-faithful: same inputs → same tag, so re-deploys of the same
// config don't churn overlay images. Uses kit.SortStrings (== the core sortStrings, byte-identical).
func overlayTagFor(base string, layers []string) string {
	sorted := append([]string(nil), layers...)
	kit.SortStrings(sorted)
	h := sha256.New()
	h.Write([]byte(base))
	h.Write([]byte{0})
	for _, l := range sorted {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// candyByName is the bare-name candy lookup over the deploykit.Generator.Candies map — the EX-core
// `Generator.candyByName` (charly/generate.go, since deleted) replicated byte-faithfully. The Candies map is keyed
// by CandyMapKey (the full scanned-set key for a remote candy, the bare name for a local), so a
// bare-name lookup MISSES for a remote add_candy candy + falls back to matching GetName()==bare.
// Every call site that holds a bare candy name + needs the CandyModel goes through here, so a
// remote add_candy overlay layer resolves instead of being silently skipped.
func candyByName(candies map[string]deploykit.CandyModel, bare string) deploykit.CandyModel {
	if c := candies[bare]; c != nil {
		return c
	}
	for _, c := range candies {
		if c != nil && c.GetName() == bare {
			return c
		}
	}
	return nil
}

// translateHostPathToVenue maps a host-side absolute path to the equivalent path inside a parent
// venue, by walking the parent deploy node's bind-mount volumes. The EX-core
// `PodDeployTarget.translateHostPathToVenue` body, byte-faithful, adapted to take the bind-mount
// volume list (reply.ParentVolumes — the host prep copied parentNode.Volume) instead of the
// package-main *FleetNode. Returns (venuePath, true) when a containing bind-mount is found;
// ("", false) otherwise. Used by the nested pod-in-pod overlay build: the nested podman runs in
// the parent venue + needs build-context paths expressed in the venue's filesystem view.
func translateHostPathToVenue(hostPath string, vols []spec.DeployVolume) (string, bool) {
	if hostPath == "" {
		return "", false
	}
	// Normalize the input: the bind-mount Host fields are typically expanded (no ~), absolute,
	// + lack trailing slashes.
	clean := filepath.Clean(hostPath)
	for _, v := range vols {
		if v.Type != "bind" || v.Host == "" || v.Path == "" {
			continue
		}
		hostBase := filepath.Clean(v.Host)
		// hostPath must equal hostBase or be a subpath of it.
		if clean == hostBase {
			return filepath.Clean(v.Path), true
		}
		prefix := hostBase + string(filepath.Separator)
		if after, ok := strings.CutPrefix(clean, prefix); ok {
			return filepath.Join(v.Path, after), true
		}
	}
	return "", false
}

// appendUnique appends items to dst, skipping duplicates. The EX-core helper
// (charly/security.go), inlined byte-faithfully — a pure dedup the candy's security-label merge
// uses. Core keeps its own copy (the config-write security surface); the candy keeps its own
// because it cannot import charly core (R3 — cross-module reuse).
func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range items {
		if !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}
