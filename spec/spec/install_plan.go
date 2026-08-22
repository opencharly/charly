package spec

// install_plan.go — the InstallPlan IR E-envelope (P4): the top-level ordered
// step container an out-of-process deploy/step plugin resolves against, plus the
// structural helpers that touch ONLY the InstallStep interface (never a concrete
// step type). The step VOCABULARY (the 13 concrete structs) and the operations
// that must type-switch it (ResolveHome, WireView) live in sdk/deploykit — an
// envelope in spec, the vocabulary + its mechanisms in deploykit.

// InstallPlan is the full ordered list of steps for one candy or one
// whole-image deploy. Compiled by the deploykit compiler and consumed by any
// DeployTarget implementation.
//
// The compiler produces one InstallPlan per candy (then merges them in
// topological order for whole-image deploys). A whole-image deploy keeps
// candy boundaries visible so the ledger can refcount which candies participate.
type InstallPlan struct {
	// Identity — populated by the compiler.
	DeployID string // per-deploy unique ID (hash of image + add_candy list)
	Box      string // deployable box name (or candy name for single-candy deploys)
	Version  string // candy/box CalVer version
	Distro   string // resolved host distro tag, e.g. "fedora:43"
	Candy    string // candy name when this plan is for a single candy; "" for whole-image merges

	// The ordered step sequence.
	Steps []InstallStep

	// Provenance — used by teardown and status.
	CandiesIncluded []string          // ordered layer names this plan composes (for whole-image merges)
	AddCandies      []string          // layers added on top via charly.yml add_layers: (for provenance)
	BuilderImage    string            // selected builder image for VenueContainerBuilder steps
	Meta            map[string]string // free-form metadata (builder image, glibc version, …)
}

// StepsByVenue partitions the plan's steps by (Scope, Venue) tuple while
// preserving intra-partition order. Host target emission uses this to
// batch contiguous same-(scope, venue) runs into one heredoc. Not used
// by the OCI target (it walks Steps directly). Uses only the InstallStep
// interface (Scope()/Venue()), so it belongs with the envelope.
func (p *InstallPlan) StepsByVenue() []StepBatch {
	if len(p.Steps) == 0 {
		return nil
	}
	out := []StepBatch{}
	cur := StepBatch{Scope: p.Steps[0].Scope(), Venue: p.Steps[0].Venue()}
	for _, s := range p.Steps {
		if s.Scope() != cur.Scope || s.Venue() != cur.Venue {
			if len(cur.Steps) > 0 {
				out = append(out, cur)
			}
			cur = StepBatch{Scope: s.Scope(), Venue: s.Venue()}
		}
		cur.Steps = append(cur.Steps, s)
	}
	if len(cur.Steps) > 0 {
		out = append(out, cur)
	}
	return out
}

// StepBatch is a contiguous run of steps sharing the same (Scope, Venue).
// Emitted together: one sudo heredoc, one user heredoc, or one podman run
// per batch.
type StepBatch struct {
	Scope Scope
	Venue Venue
	Steps []InstallStep
}

// DeployTarget is the interface OCI + container-deploy + host-deploy
// emitters satisfy. Taking a slice of plans (rather than a single plan)
// lets whole-image deploys pass all per-candy plans at once and let the
// target merge them — useful because OCITarget may want to emit a single
// Containerfile for the image while the local deploy target may batch steps
// across candies.
type DeployTarget interface {
	Name() string
	Emit(plans []*InstallPlan, opts EmitOpts) error
}
