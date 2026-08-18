package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// candy_select.go — the K4 unit B candy-half: the STANDALONE-candy selection
// (the former host-side compileCandySelection, since-deleted from charly/ core, ctx==nil case only —
// a target-only deploy with no base image, i.e. `target: local`/`vm`/every external
// substrate) now runs entirely plugin-side, using data already sitting in the
// resolved-project envelope compileDeployPlans fetches — no LoadUnified, no registry,
// no new HostBuild kind. The BOX-selection shape (compileBoxSelection, and add_candy
// compiled against a pod/kubernetes BASE image's context) is UNCHANGED and still sends a
// host-computed BoxView+Order; see #DeployCompileRequest's doc comment in
// sdk/schema/seam.cue for the two-shape discrimination.
//
// Mechanism precedent this reuses (R3, nothing invented here):
//   - the candy scan + topo order is deploykit.ResolveCandyOrder over rp.CandyModels —
//     already a pure sdk/deploykit function (candy/plugin-fleet's own compile.go loop
//     already builds the SAME candyModels map from rp.CandyModels/rp.Candies);
//   - the vm-target synthetic box resolves its kind:vm template via the kind:vm
//     provider's own OpResolve leg — the EXACT sdk.Executor.InvokeProvider(class:"kind",
//     word:"vm"/"local", OpResolve, …) pattern K4 unit A's lookupLocalTemplate
//     (node_resolve.go) already proved live (check-preempt-local R10);
//   - the host-target synthetic box resolves via vmshared.DetectHostDistro/
//     DetectHostGlibc — already sdk-portable (charly core references vmshared.DetectHostDistro
//     directly now — no charly-core alias).

// envelopeCandyModels re-hydrates every candy in the envelope into the map[string]spec.CandyReader
// deploykit.ResolveCandyOrder wants (rp.Candies/rp.CandyModels is nil-free BY CONSTRUCTION —
// sdk/loaderkit's ProjectResolvedProject (the shared assembler candy/plugin-build's
// resolveBuildEngine and the host's own test-only reproduction both call into) skips a nil
// host-scan entry before it ever enters the envelope, `if c == nil { continue }` — so this map
// matches the OLD host scan's "layers[name] != nil" filtered view exactly, without a separate
// filter step here).
// Shared (R3) by the CANDY shape (resolveCandySelection) and the BOX-REF shape
// (resolveBoxSelection) — both need the FULL candy map, not just their own final order, since
// deploykit.ResolveCandyOrder needs the whole graph to expand composition + transitive deps.
func envelopeCandyModels(rp *spec.ResolvedProject) map[string]spec.CandyReader {
	candyModels := make(map[string]spec.CandyReader, len(rp.CandyModels))
	for name, cm := range rp.CandyModels {
		cv, ok := rp.Candies[name]
		if !ok {
			continue
		}
		candyModels[name] = deploykit.NewSpecCandyModel(cm, cv)
	}
	return candyModels
}

// resolveCandySelection computes the topo-sorted candy order for a standalone candy
// deploy (req.CandyRef) from the already-fetched envelope, and the synthetic
// *buildkit.ResolvedBox to compile it against (vm-tuned when req.VmEntity resolves
// against the envelope, a plain host box otherwise — TOLERANTLY, see
// syntheticBoxForCandySelection). Mirrors charly/fleet_add_cmd.go's former
// syntheticHostBox/syntheticVmBox exactly (same field derivation), operating on the
// envelope's rp.Distro/rp.CandyModels/rp.Templates.VM instead of a live *Config/
// *spec.DistroConfig/LoadUnified.
func resolveCandySelection(ctx context.Context, exec *sdk.Executor, rp *spec.ResolvedProject, req spec.DeployCompileRequest) ([]string, *buildkit.ResolvedBox, error) {
	candyKey := deploykit.BareRef(req.CandyRef)
	candyModels := envelopeCandyModels(rp)
	if _, ok := candyModels[candyKey]; !ok {
		return nil, nil, fmt.Errorf("candy %q not in resolved-project envelope", req.CandyRef)
	}
	order, err := deploykit.ResolveCandyOrder([]string{candyKey}, candyModels, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving deps for %s: %w", req.CandyRef, err)
	}

	img := syntheticBoxForCandySelection(ctx, exec, rp, req.VmEntity)
	return order, img, nil
}

// syntheticBoxForCandySelection picks the vm-tuned or plain host synthetic box, TOLERANTLY —
// mirrors the OLD host-side compileCandySelection's vm lookup exactly (RCA'd live via
// check-preempt-local: resolveVmEntity threads node.From into vmEntity UNCONDITIONALLY, not only
// for a genuine vm cross-ref — e.g. a `local: {from: check-preempt-app}` node's kind:local
// template ref lands in vmEntity too — so a miss/resolve-failure here is the COMMON case, not an
// error condition; a hard error on a miss was the exact bug this RCA caught and fixed).
func syntheticBoxForCandySelection(ctx context.Context, exec *sdk.Executor, rp *spec.ResolvedProject, vmEntity string) *buildkit.ResolvedBox {
	if vmEntity != "" {
		if img, err := syntheticVmBoxFromEnvelope(ctx, exec, rp, vmEntity); err == nil && img != nil {
			return img
		}
	}
	return syntheticHostBoxFromEnvelope(rp.Distro, rp.Builder)
}

// syntheticHostBoxFromEnvelope mirrors charly/fleet_add_cmd.go's syntheticHostBox — a
// pure function of the OPERATOR's own process env/uid/gid plus a host distro probe, plus the
// envelope's distro/builder vocabulary (distro/builder, rp.Distro/rp.Builder). DistroConfig/
// DistroDef and BuilderConfig are set UNCONDITIONALLY — matching NewSpecResolvedBox/
// resolveBoxSelection's own box-construction path (R3) — so a standalone-candy deploy with no
// base image (target: local/vm/every external substrate) still gets its system packages
// installed AND detects an npm/pixi/cargo/aur-triggering candy's builder step. A synthetic box
// with a nil DistroDef made CompileSystemPackageSteps early-return zero steps (#118 task #59:
// the guest's system packages — e.g. the virtualization candy's libvirt — silently never
// install, while the DISTINCT distro:-gated ServicePackaged step still fires since its gate
// reads img.Distro, not img.DistroDef — a masked inconsistency: check-charly-vm's
// deploy-add failed enabling virtqemud.socket because libvirt was never installed). A nil
// BuilderConfig made compileBuilderSteps early-return zero steps the same way (#118 task #56:
// deploy-check-builder-member reported PASS with a zero-step compiled plan).
func syntheticHostBoxFromEnvelope(distro map[string]*spec.ResolvedDistro, builder map[string]*spec.Builder) *buildkit.ResolvedBox {
	hd, _ := vmshared.DetectHostDistro()
	img := &buildkit.ResolvedBox{
		ResolvedBox: spec.ResolvedBox{
			Name:         "host-adhoc",
			Home:         os.Getenv("HOME"),
			User:         os.Getenv("USER"),
			UID:          os.Getuid(),
			GID:          os.Getgid(),
			BuildFormats: []string{},
		},
		BuilderConfig: &spec.BuilderConfig{Builder: builder},
	}
	if hd != nil {
		img.Distro = append(img.Distro, hd.Tags...)
		if hint := hd.FormatHint(); hint != "" {
			img.Pkg = hint
			img.BuildFormats = []string{hint}
		}
	}
	img.DistroConfig = &spec.DistroConfig{Distro: distro}
	img.DistroDef = img.DistroConfig.ResolveDistro(img.Distro)
	return img
}

// syntheticVmBoxFromEnvelope mirrors charly/fleet_add_cmd.go's syntheticVmBox exactly
// (same field derivation, same distro-cascade + inherit_packages expansion), reading
// the vm entity off rp.Templates.VM (the SAME opaque RawBody map the "resolved-project"
// envelope already carries) and resolving it via the kind:vm provider's own OpResolve
// leg (the vm-substrate sibling of K4 unit A's kind:local reuse), and the distro
// cascade off rp.Distro (a plain map[string]*spec.ResolvedDistro — spec.DistroConfig
// is just a thin method-holder over that SAME map, so reconstructing one here from the
// envelope's copy is byte-identical to the host's live *spec.DistroConfig).
func syntheticVmBoxFromEnvelope(ctx context.Context, exec *sdk.Executor, rp *spec.ResolvedProject, vmEntity string) (*buildkit.ResolvedBox, error) {
	var raw spec.RawBody
	if rp.Templates != nil {
		raw = rp.Templates.VM[vmEntity]
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no kind:vm entity named %q in the resolved-project envelope", vmEntity)
	}
	vmSpec, err := resolveVmTemplateViaPlugin(ctx, exec, raw)
	if err != nil {
		return nil, fmt.Errorf("resolving kind:vm entity %q: %w", vmEntity, err)
	}
	if vmSpec == nil {
		return nil, fmt.Errorf("kind:vm entity %q resolved to an empty value", vmEntity)
	}
	return buildVmSyntheticBox(vmSpec, rp.Distro, rp.Builder), nil
}

// buildVmSyntheticBox is the PURE field-derivation half of syntheticVmBoxFromEnvelope, split out
// (K4 unit B) so it is directly unit-testable without a live kind:vm provider RPC — mirrors
// charly/synthetic_vm_image_test.go's former TestSyntheticVmImageDistroFormat/
// TestSyntheticVmImageRootFallback coverage (relocated to candy_select_test.go), the regression
// guard for the non-arch VM deploy bug: a naive synthetic box used to hardcode
// Distro:["arch"]/Pkg:"pac" for EVERY non-root VM, so a candy deploy onto a debian/ubuntu/fedora
// guest ran `pacman` and failed with exit 127. distro is rp.Distro (a plain
// map[string]*spec.ResolvedDistro — spec.DistroConfig is just a thin method-holder over that
// SAME map, so reconstructing one here from the envelope's copy is byte-identical to the host's
// former live *spec.DistroConfig). builder is rp.Builder, threaded through so this synthetic
// vm-adhoc box also gets BuilderConfig set (#118 task #56 — see syntheticHostBoxFromEnvelope).
// DistroConfig/DistroDef are ALSO set unconditionally (#118 task #59 — see
// syntheticHostBoxFromEnvelope): without DistroDef, CompileSystemPackageSteps silently compiled
// zero steps for EVERY candy on a standalone vm-adhoc deploy, while the distro:-gated
// ServicePackaged step (reading img.Distro, a DIFFERENT field) still fired — RCA'd live via
// check-charly-vm's deploy-add failing to enable virtqemud.socket because libvirt was never
// installed.
func buildVmSyntheticBox(vmSpec *spec.ResolvedVm, distro map[string]*spec.ResolvedDistro, builder map[string]*spec.Builder) *buildkit.ResolvedBox {
	user := vmshared.ResolveCloudInitSSHUser(vmSpec)
	if user == "" || user == "root" {
		img := syntheticHostBoxFromEnvelope(distro, builder)
		img.Name = "vm-adhoc"
		img.User = "root"
		img.Home = "/root"
		return img
	}
	img := &buildkit.ResolvedBox{
		ResolvedBox: spec.ResolvedBox{
			Name: "vm-adhoc",
			User: user,
			UID:  1000,
			GID:  1000,
			Home: "/home/" + user,
		},
		BuilderConfig: &spec.BuilderConfig{Builder: builder},
	}
	// The distro comes from the EXPLICIT field only. There is deliberately no fallback to
	// BaseUser: an account name is not a distro, and inferring one from the other is what left a
	// Debian-family guest rendered with Arch package names. The vm kind's OpValidate requires
	// source.distro on every distro-bearing source arm, so an empty value here is an authoring
	// error that was already rejected upstream — not a case to guess at.
	distroKey := vmSpec.Source.Distro
	distroCfg := &spec.DistroConfig{Distro: distro}
	if distroKey != "" {
		if def := distroCfg.ResolveDistro([]string{distroKey}); def != nil {
			img.Distro = distroCfg.ExpandPackageInheritance(spec.DistroTagChain(distroKey, def.Version))
			if pf := def.PrimaryFormat(); pf != "" {
				img.Pkg = pf
				img.BuildFormats = []string{pf}
			}
		} else {
			img.Distro = []string{distroKey}
		}
	}
	img.DistroConfig = distroCfg
	img.DistroDef = distroCfg.ResolveDistro(img.Distro)
	return img
}

// resolveVmTemplateViaPlugin projects one opaque vm template body into a *spec.ResolvedVm
// via the kind:vm provider's own OpResolve leg — the plugin↔plugin peer dispatch
// (sdk.Executor.InvokeProvider) K4 unit A's node_resolve.go/lookupLocalTemplate already
// proved live for the sibling kind:local case (same substrate provider, candy/plugin-substrate,
// serves both words).
func resolveVmTemplateViaPlugin(ctx context.Context, exec *sdk.Executor, raw spec.RawBody) (*spec.ResolvedVm, error) {
	reqJSON, err := json.Marshal(spec.SubstrateTemplateResolveRequest{Vm: &spec.VmResolveInput{Vm: raw}})
	if err != nil {
		return nil, err
	}
	resJSON, err := exec.InvokeProvider(ctx, "kind", "vm", sdk.OpResolve, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, err
	}
	var reply spec.VmResolveReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return nil, fmt.Errorf("decode kind:vm resolve reply: %w", err)
		}
	}
	return reply.Resolved, nil
}
