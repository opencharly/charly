package check

// live_scoring.go — K1-unblock W3 Unit A: the runner-INDEPENDENT half of
// charly/check_runner_live.go's live-scoring machinery (topo-sort / same-venue bucketing /
// dependency-cascade skip / ephemeral-deploy classification). Pure over spec.Step +
// map[string]spec.BundleNode — no core-only dependency, confirmed by reading the original in
// full: none of these touch newCheckRunner.
//
// NOT ported (stay charly/check_runner_live.go, Unit B territory): RunCheckLive/
// scoreOnePodBucket/resolveScoringChain — they construct a kit.Runner via newCheckRunner, whose
// cfg.Verbs = &hostVerbResolver{} holds the core-private providerRegistry (the clause-M
// plugin-word-dispatch mechanism); moving them needs the new "check-run-execute" HostBuild leaf.
//
// Also NOT ported: RenderPlanYAML / isScored / scoredPlanOrigin — the plugin already carries its
// own copies (harness_loop.go / score.go); charly/check_runner_live.go's RenderPlanYAML is
// confirmed DEAD (zero callers anywhere in package main) and is deleted, not moved, when this
// family's core files are cut over.

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// scoredStep pairs a plan step with its stable declaration-order id so the ids stay consistent
// across live scoring regardless of the topo/bucket execution reorder.
type scoredStep struct {
	id   string
	step spec.Step
}

// scoredSteps wraps a plan with stable ids by declaration order.
func scoredSteps(plan []spec.Step) []scoredStep {
	out := make([]scoredStep, len(plan))
	for i := range plan {
		out[i] = scoredStep{id: kit.EffectiveStepID(&plan[i], scoredPlanOrigin, i), step: plan[i]}
	}
	return out
}

// topoSortScored orders scored steps by depends_on (id-keyed), returning the ordered slice and,
// on cycle, the non-cyclic remainder + the cyclic entries.
func topoSortScored(entries []scoredStep) (ordered, cyclic []scoredStep) {
	idToIdx := make(map[string]int, len(entries))
	for i, e := range entries {
		idToIdx[e.id] = i
	}
	indeg := make([]int, len(entries))
	fwd := make([][]int, len(entries))
	for i, e := range entries {
		for _, dep := range e.step.DependsOn {
			if d, ok := idToIdx[dep]; ok {
				fwd[d] = append(fwd[d], i)
				indeg[i]++
			}
		}
	}
	var ready []int
	for i, n := range indeg {
		if n == 0 {
			ready = append(ready, i)
		}
	}
	sortIntsAsc(ready)
	for len(ready) > 0 {
		head := ready[0]
		ready = ready[1:]
		ordered = append(ordered, entries[head])
		for _, succ := range fwd[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				ready = append(ready, succ)
				sortIntsAsc(ready)
			}
		}
	}
	if len(ordered) != len(entries) {
		fmt.Fprintf(os.Stderr, "score live: dependency cycle detected — scoring non-cyclic steps; cyclic steps reported as fail verdicts\n")
		for i, n := range indeg {
			if n > 0 {
				cyclic = append(cyclic, entries[i])
			}
		}
	}
	return ordered, cyclic
}

func sortIntsAsc(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// groupScoredByPod splits sorted scored steps into maximal same-pod runs.
func groupScoredByPod(sorted []scoredStep) [][]scoredStep {
	if len(sorted) == 0 {
		return nil
	}
	var buckets [][]scoredStep
	cur := []scoredStep{sorted[0]}
	curPod := sorted[0].step.Venue
	for _, e := range sorted[1:] {
		if e.step.Venue == curPod {
			cur = append(cur, e)
			continue
		}
		buckets = append(buckets, cur)
		cur = []scoredStep{e}
		curPod = e.step.Venue
	}
	buckets = append(buckets, cur)
	return buckets
}

func bucketSteps(b []scoredStep) []spec.Step {
	out := make([]spec.Step, len(b))
	for i, e := range b {
		out[i] = e.step
	}
	return out
}

// skippedStepScore builds a depends-on-cascade skip result for one scored step.
func skippedStepScore(e scoredStep, pod, blockedBy string) spec.StepScore {
	return spec.StepScore{
		ID:            e.id,
		Origin:        "pod:" + pod,
		Text:          e.step.KeywordText(),
		Tag:           kit.EffectiveTags(e.step.Tag),
		Status:        "skipped",
		SkippedReason: "dep-unmet: " + blockedBy,
	}
}

// isEphemeralDeploy reports whether the named pod resolves to a charly.yml entry marked
// ephemeral.
func isEphemeralDeploy(roots map[string]spec.BundleNode, pod string) bool {
	if pod == "" {
		return false
	}
	if node, ok := roots[pod]; ok {
		return node.IsEphemeral()
	}
	if node, _, err := deploykit.ResolveNodePath(roots, pod); err == nil && node != nil {
		return node.IsEphemeral()
	}
	return false
}

// ephemeralKeepOnFailure returns the keep_on_failure flag from the named ephemeral deploy's
// lifetime block.
func ephemeralKeepOnFailure(roots map[string]spec.BundleNode, pod string) bool {
	if pod == "" {
		return false
	}
	resolve := func(node *spec.BundleNode) bool {
		if node == nil || node.Ephemeral == nil {
			return false
		}
		return node.Ephemeral.KeepOnFailure
	}
	if node, ok := roots[pod]; ok {
		return resolve(&node)
	}
	if node, _, err := deploykit.ResolveNodePath(roots, pod); err == nil {
		return resolve(node)
	}
	return false
}
