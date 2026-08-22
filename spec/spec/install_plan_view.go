package spec

// install_plan_view.go — the plan-level IR mechanisms that type-switch the concrete
// step vocabulary: WireView / PlanFromView (the InstallPlan ⇄ InstallPlanView bridge),
// ResolveHome (deferred-home substitution), GateEnabled, and the HomeToken constant.
//
// Relocated from sdk/deploykit (#55 step-4). They live beside the IR envelope
// (InstallPlan, install_plan.go) + the step vocabulary (install_step_vocab.go) they
// type-switch, so spec owns the whole in-proc IR + its mechanisms; deploykit keeps
// thin `var WireView = spec.WireView` aliases.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HomeToken is the deferred-home placeholder resolved by ResolveHome at emit time.
const HomeToken = "{{.Home}}"

// WireView projects the rich in-core InstallPlan onto the JSON-roundtrippable
// InstallPlanView the host marshals into an external deploy/step provider's
// op.Params. The Steps interface slice round-trips through the SINGLE StepsToView /
// stepsFromView converter (install_step_view.go) — an external deploy/step plugin walks the
// same ordered step IR the in-proc DeployTargets walk and EXECUTES it on the venue (R3;
// proven by the step-IR round-trip test). The remaining fields are identity + provenance.
//
// A free function (not a method on InstallPlan) because it type-switches the
// concrete step vocabulary via StepsToView.
func WireView(p *InstallPlan) InstallPlanView {
	if p == nil {
		return InstallPlanView{}
	}
	return InstallPlanView{
		DeployID:        p.DeployID,
		Box:             p.Box,
		Version:         p.Version,
		Distro:          p.Distro,
		Candy:           p.Candy,
		CandiesIncluded: p.CandiesIncluded,
		AddCandies:      p.AddCandies,
		BuilderImage:    p.BuilderImage,
		Meta:            p.Meta,
		Steps:           StepsToView(p.Steps),
	}
}

// PlanFromView re-materializes the rich in-core *InstallPlan from its JSON-roundtrippable
// InstallPlanView wire form — the REVERSE of WireView, used by the host to reconstruct
// []*InstallPlan from the command:fleet plugin's OpCompile reply (K4-B). Steps round-trip
// through the SINGLE stepsFromView converter (install_step_view.go), already proven round-trip-faithful
// by TestStepView_RoundTrip. The host re-materialized plan is byte-equivalent to the former
// in-proc compile output (the K4-B parity golden proves it via DeepEqual against the OLD
// host-compile path).
// PlansFromViews decodes a marshalled []InstallPlanView (the wire shape a command plugin ships for
// already-compiled plans) and re-materializes each into an *InstallPlan — the resolve-target-add
// seam's per-view loop, consolidated here (K-wave 2 cone R2 bank D thin).
func PlansFromViews(viewsJSON json.RawMessage) ([]*InstallPlan, error) {
	var views []InstallPlanView
	if len(viewsJSON) > 0 {
		if err := json.Unmarshal(viewsJSON, &views); err != nil {
			return nil, fmt.Errorf("decode compiled plans: %w", err)
		}
	}
	plans := make([]*InstallPlan, 0, len(views))
	for _, v := range views {
		p, perr := PlanFromView(v)
		if perr != nil {
			return nil, perr
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// ReconstructParentExec re-derives the ancestor executor chain from ROOT-FIRST ancestor
// path/node lists (deploykit.ResolveNodePath's contract, EXCLUDING the target itself), applying
// derive to each ancestor. derive is the registry-coupled per-ancestor hop (core's
// deriveChildExecutorForPath) — the loop itself is pure, so it lives here once (K-wave 2 cone R2
// bank D thin: the resolve-target-add seam's former reconstructParentExec).
func ReconstructParentExec(ancestorPaths []string, ancestorNodes []FleetNode, derive func(string, *FleetNode, DeployExecutor) (DeployExecutor, error)) (DeployExecutor, error) {
	var parentExec DeployExecutor
	for i, ap := range ancestorPaths {
		var anc *FleetNode
		if i < len(ancestorNodes) {
			anc = &ancestorNodes[i]
		}
		next, err := derive(ap, anc, parentExec)
		if err != nil {
			return nil, fmt.Errorf("deriving executor for ancestor %q: %w", ap, err)
		}
		parentExec = next
	}
	return parentExec, nil
}

func PlanFromView(v InstallPlanView) (*InstallPlan, error) {
	steps, err := stepsFromView(v.Steps)
	if err != nil {
		return nil, fmt.Errorf("re-materialize plan %q: %w", v.Candy, err)
	}
	return &InstallPlan{
		DeployID:        v.DeployID,
		Box:             v.Box,
		Version:         v.Version,
		Distro:          v.Distro,
		Candy:           v.Candy,
		CandiesIncluded: v.CandiesIncluded,
		AddCandies:      v.AddCandies,
		BuilderImage:    v.BuilderImage,
		Meta:            v.Meta,
		Steps:           steps,
	}, nil
}

// ResolveHome substitutes the deferred HomeToken with a concrete home in
// every home-bearing step field, in place. Each DeployTarget calls this once
// at emit time with the home of its real destination: img.Home for the
// OCI/pod-overlay build, the host home for the external local deploy, the GUEST home
// (SSH executor ResolveHome) for the external vm deploy. Idempotent — fields without
// the token are left untouched, so a second call is a no-op.
//
// Covered fields: ShellHookStep env values + PathAdd, ShellSnippetStep Snippet
// + Destination + PathAppend, FileStep.Dest. OpStep cmd/content bodies are
// intentionally NOT touched — `~`/`$HOME` there shell-expand at runtime on the
// destination as the deploy user, which is already correct on every venue.
// BuilderStep is also untouched — its home is resolved separately by
// renderBuilderScript against the builder/guest home (see execBuilder).
//
// A free function (not a method on InstallPlan) because it type-switches the
// concrete step vocabulary.
func ResolveHome(p *InstallPlan, home string) {
	if p == nil || home == "" {
		return
	}
	sub := func(s string) string { return strings.ReplaceAll(s, HomeToken, home) }
	for _, step := range p.Steps {
		switch s := step.(type) {
		case *ShellHookStep:
			for k, v := range s.EnvVars {
				s.EnvVars[k] = sub(v)
			}
			for i, pth := range s.PathAdd {
				s.PathAdd[i] = sub(pth)
			}
		case *ShellSnippetStep:
			s.Snippet = sub(s.Snippet)
			s.Destination = sub(s.Destination)
			for i, pth := range s.PathAppend {
				s.PathAppend[i] = sub(pth)
			}
		case *FileStep:
			s.Dest = sub(s.Dest)
		case *ServiceCustomStep:
			// The systemd unit is pre-rendered at compile with {{.Home}} for
			// host/vm targets (see compileServiceSteps); resolve it — and the
			// user-scope unit install path — against the destination home here.
			s.UnitText = sub(s.UnitText)
			s.UnitPath = sub(s.UnitPath)
		case *OpStep:
			// Home-relative copy/download dest (tokenized at compile). The
			// Task body itself (cmd/content) is left alone — those shell-expand
			// $HOME at runtime as the deploy user.
			s.To = sub(s.To)
		}
	}
}

// GateEnabled returns whether the given gate is permitted under opts.
// GateNone is always enabled; named gates require the corresponding
// opt-in flag.
func GateEnabled(g Gate, opts EmitOpts) bool {
	switch g {
	case GateNone:
		return true
	case GateAllowRepoChanges:
		return opts.AllowRepoChanges || opts.AssumeYes
	case GateAllowRootTasks:
		return opts.AllowRootTasks || opts.AssumeYes
	case GateWithServices:
		return opts.WithServices || opts.AssumeYes
	}
	return false
}
