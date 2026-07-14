package box

// validate.go — the `charly box validate` ENGINE (task #60, Unit C). The validation engine moved OUT
// of charly core INTO this compiled-in plugin: dispatchValidate fetches the error-TOLERANT resolved
// project ENVELOPE from the host (HostBuild("validate-project") → spec.ValidateProjectReply) and runs
// EVERY pure per-kind/op rule + the deploykit resolution-graph checks over that envelope — reading
// spec.CandyModel / spec.CandyView / spec.ResolvedBoxView INSTEAD of the runtime *Candy/*Config graph.
// The host keeps ONLY what a plugin structurally cannot do (the CUE-conformance / build-tunable / merge
// / base⊻from / remote-candy checks — already emitted as reply.Diagnostics by validate_project_host.go);
// the plugin MERGES those host diagnostics with its own findings for the final verdict + exit code.
//
// It imports ONLY the sdk module (sdk, sdk/spec, sdk/kit, sdk/deploykit, sdk/buildkit, sdk/vmshared) —
// never charly core. Pure helpers the core validator kept (findSimilarName / levenshtein /
// isAbsOrHomePath / … ) are COPIED verbatim (a pure helper duplicated across the core/plugin MODULE
// boundary is fine, like sdk/kit).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// dispatchValidate runs the `charly box validate` engine. It parses the grammar, fetches the
// error-TOLERANT resolved-project envelope from the host (the fully-resolved project the plugin cannot
// load itself pre-K1), builds the validation context, runs every ported rule, MERGES the host
// diagnostics with the plugin findings, and returns a plain error in the core ValidationError format
// when anything failed (Kong then prints `charly: error: command "validate": …`, exit 1) — else nil → exit 0.
func dispatchValidate(hc *hostClient, args []string) error {
	var g validateGrammar
	if done, err := parseLeaf("validate", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	merged, err := runValidateEngine(hc.ctx, hc.exec, dir, g.IncludeDisabled)
	if err != nil {
		return err
	}
	return emitVerdict(merged)
}

// runValidateEngine fetches the error-TOLERANT resolved-project envelope + host diagnostics
// (HostBuild("validate-project")), runs every ported rule over the envelope, and returns the MERGED
// spec.Diagnostics — the host-natural + resolve diagnostics UNION the plugin's per-kind/op findings.
// Shared by the `charly box validate` CLI verdict (dispatchValidate → emitVerdict) AND the pre-build
// gate structured op (Invoke(OpValidate), consumed by core generate.go). ctx/exec are the reverse
// channel to the host (in-proc when compiled-in).
func runValidateEngine(ctx context.Context, exec *sdk.Executor, dir string, includeDisabled bool) (spec.Diagnostics, error) {
	reqJSON, err := json.Marshal(spec.ValidateProjectRequest{Dir: dir, IncludeDisabled: includeDisabled})
	if err != nil {
		return spec.Diagnostics{}, err
	}
	resJSON, err := exec.HostBuild(ctx, validateProjectBuilderKind, reqJSON)
	if err != nil {
		return spec.Diagnostics{}, err
	}
	var reply spec.ValidateProjectReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return spec.Diagnostics{}, fmt.Errorf("box validate: decode reply: %w", uerr)
		}
	}
	vc := newVctx(reply.Project)
	e := &vErr{}
	runAllValidations(vc, e)
	merged := reply.Diagnostics
	for _, m := range e.msgs {
		merged.Items = append(merged.Items, spec.Diagnostic{Severity: "error", Message: m})
	}
	return merged, nil
}

// validateProjectBuilderKind is the F11 host-builder kind the host serves the tolerant
// resolved-project projection + host-natural diagnostics under (validate_project_host.go). A generic
// action noun, never a provider word.
const validateProjectBuilderKind = "validate-project"

// vErr accumulates the plugin's own validation findings (the host's ride in reply.Diagnostics).
type vErr struct{ msgs []string }

// Add appends one formatted finding.
func (e *vErr) Add(format string, a ...any) { e.msgs = append(e.msgs, fmt.Sprintf(format, a...)) }

// vctx is the validation context: the resolved-project envelope plus the two deploykit/buildkit
// adapters the resolution-graph rules run over. models/views/boxes are the envelope maps verbatim;
// dk wraps each candy's (model,view) into a deploykit.CandyModel (the interface ResolveCandyOrder /
// ExpandCandy / GlobalCandyOrder consume); bk projects each resolved box view into a
// buildkit.ResolvedBox (what ResolveBoxOrder consumes).
type vctx struct {
	env    *spec.ResolvedProject
	models map[string]spec.CandyModel
	views  map[string]spec.CandyView
	boxes  map[string]spec.ResolvedBoxView
	dk     map[string]deploykit.CandyModel
	bk     map[string]*buildkit.ResolvedBox
}

// newVctx builds the validation context from the (possibly nil / partial) resolved-project envelope.
// A nil project (project-less dir) yields an empty context — every rule loops zero times and the
// verdict is whatever host diagnostics carried.
func newVctx(rp *spec.ResolvedProject) *vctx {
	vc := &vctx{
		env:    rp,
		models: map[string]spec.CandyModel{},
		views:  map[string]spec.CandyView{},
		boxes:  map[string]spec.ResolvedBoxView{},
		dk:     map[string]deploykit.CandyModel{},
		bk:     map[string]*buildkit.ResolvedBox{},
	}
	if rp == nil {
		return vc
	}
	for name, m := range rp.CandyModels {
		vc.models[name] = m
	}
	for name, v := range rp.Candies {
		vc.views[name] = v
	}
	for name, b := range rp.Boxes {
		vc.boxes[name] = b
	}
	// dk: the deploykit CandyModel adapter per candy (build model + identity/graph view + live fs probes).
	for name, m := range rp.CandyModels {
		vc.dk[name] = deploykit.NewSpecCandyModel(m, rp.Candies[name])
	}
	// bk: the buildkit ResolvedBox projection per resolved box (the resolution-graph fns' input).
	for name, b := range rp.Boxes {
		vc.bk[name] = viewToBuildkit(b)
	}
	return vc
}

// viewToBuildkit projects a resolved-box VIEW back into a buildkit.ResolvedBox — the exact fields the
// resolution-graph functions (ResolveBoxOrder / BoxDirectDeps / BoxNeedsBuilder / CandyProvidedByBox)
// read: identity + base/from edges + builder map + candy list + user/data flags. Merge and the six
// json:"-" host-only compute caches (DistroConfig/DistroDef/BuilderConfig/InitSystem/InitDef/CandyCaps)
// are left nil — the graph functions never read them.
func viewToBuildkit(v spec.ResolvedBoxView) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{
		Name:                  v.Name,
		Version:               v.Version,
		EffectiveVersion:      v.EffectiveVersion,
		Status:                v.Status,
		Info:                  v.Info,
		CheckLevel:            v.CheckLevel,
		Base:                  v.Base,
		From:                  v.From,
		BootstrapBuilderImage: v.BootstrapBuilderImage,
		Platforms:             v.Platforms,
		Tag:                   v.Tag,
		Registry:              v.Registry,
		Pkg:                   v.Pkg,
		Distro:                v.Distro,
		BuildFormats:          v.BuildFormats,
		Tags:                  v.Tags,
		Candy:                 v.Candy,
		User:                  v.User,
		UID:                   int(v.UID),
		GID:                   int(v.GID),
		Home:                  v.Home,
		UserAdopted:           v.UserAdopted,
		Builder:               buildkit.BuilderMap(v.Builder),
		BuilderCapabilities:   v.BuilderCapabilities,
		Auto:                  v.Auto,
		Network:               v.Network,
		DataImage:             v.DataImage,
		IsExternalBase:        v.IsExternalBase,
		FullTag:               v.FullTag,
	}
}

// runAllValidations runs every ported rule over the envelope, in the same order the former core
// Validate() ran them (the host-natural rules — build/distro, CUE-conformance, base⊻from, merge,
// tunables, remote-candy — already ran host-side and ride in reply.Diagnostics, so they are omitted
// here). Each rule accumulates into e.
func runAllValidations(vc *vctx, e *vErr) {
	// A-pure candy + box rules (validate_rules.go).
	validateCandyReferences(vc, e)
	validateCandyContents(vc, e)
	validateCandyTasks(vc, e)
	validatePkgConfig(vc, e)
	validateVolume(vc, e)
	validateAliases(vc, e)
	validateCandyIncludes(vc, e)
	validateSystemdServices(vc, e)
	validateLibvirt(vc, e)
	validateEnvProvides(vc, e)
	validateEnvDeps(vc, e)
	validateSecretDeps(vc, e)
	validateMCPProvides(vc, e)
	validateMCPDeps(vc, e)
	validateLocalTemplates(vc, e)
	validateLocalDeployments(vc, e)

	// B-graph + capabilities rules (validate_graph.go).
	validateBoxDAG(vc, e)
	validateCandyDAG(vc, e)
	validateBuilders(vc, e)
	validatePackagedServices(vc, e)
	validateEngineConfig(vc, e)
	validatePortRelay(vc, e)
	validateDataCandies(vc, e)
	validateInitDependencies(vc, e)

	// Op-level plan validation (validate_check.go).
	validateOps(vc, e)
}

// emitVerdict returns the MERGED diagnostics (from runValidateEngine) as a plain error in the
// core ValidationError.Error() format when any error-severity finding is present (else nil → exit 0):
// "validation error: <m>" for one, "N validation errors:\n\n  <joined by \n  >" for many. The host
// wraps it `command "validate": …` and Kong prints the `charly: error:` decoration + exits 1 —
// identical to how generate/pkg surface a failure.
func emitVerdict(diags spec.Diagnostics) error {
	msgs := make([]string, 0, len(diags.Items))
	for _, it := range diags.Items {
		if it.Severity == "warning" {
			continue // the verdict counts ERROR-severity findings; the core validator emitted only errors
		}
		msgs = append(msgs, it.Message)
	}
	if len(msgs) == 0 {
		return nil
	}
	// Return the verdict as a PLAIN error in the EXACT format core ValidationError.Error() uses (the
	// string `charly box generate` returns) — so the host wraps it `command "validate": …` and Kong
	// prints the standard `charly: error:` decoration, byte-identical to how generate/pkg surface a
	// failed validation. Never print here + return ExitCodeError: that suppressed the decoration and
	// made validate the lone box subcommand formatting its errors differently.
	if len(msgs) == 1 {
		return fmt.Errorf("validation error: %s", msgs[0])
	}
	return fmt.Errorf("%d validation errors:\n\n  %s", len(msgs), strings.Join(msgs, "\n  "))
}

// candyNames returns the sorted candy names (map keys) — the typo-suggestion candidate set, mirroring
// the former core CandyNames(layers).
func candyNames(vc *vctx) []string {
	names := make([]string, 0, len(vc.models))
	for name := range vc.models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// dirExists reports whether p is an existing directory (the plugin runs in the host process, so the
// candy source trees are on the same filesystem — a live os.Stat, mirroring the core validator).
func dirExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// findSimilarName finds a close (Levenshtein ≤ 2) candidate for typo suggestions. Copied verbatim from
// the core validator (pure).
func findSimilarName(target string, candidates []string) string {
	for _, candidate := range candidates {
		if levenshteinDistance(target, candidate) <= 2 {
			return candidate
		}
	}
	return ""
}

// levenshteinDistance is the edit distance between a and b. Copied verbatim from the core validator (pure).
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,
				matrix[i][j-1]+1,
				matrix[i-1][j-1]+cost,
			)
		}
	}
	return matrix[len(a)][len(b)]
}
