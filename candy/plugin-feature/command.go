package feature

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// command.go — the externalized `charly feature` command (list / pending / validate — inspect
// plan-shaped descriptions). The plugin OWNS the subcommand grammar + the output formatting AND the
// plan-to-summary transform (keyword/text/agent/check flattening + validatePlanSteps — kit.KeywordOf /
// kit.ValidatePlanSteps / deploykit.DescriptionInfo are sdk-portable, K3). The project ENUMERATION
// (formerly the "feature" HostBuild seam's body, charly/host_build_feature.go) is now PLUGIN-SIDE:
// the loader is plugin-reachable (LoadUnifiedViaExecutor + ProjectCandiesScanned +
// FinalizeScannedCandies, K-wave 2 cone R6 — the seam is DELETED), so the plugin drives the whole
// project load itself over the reverse channel and flattens every entity's RAW description + plan
// into plain spec.FeatureEntity data. No hidden `__feature-*` forward.
//
// (The Feature RUN verbs — `charly box feature run` / `charly check feature run` — stay children of
// box/check in the core binary, NOT part of this plugin.)
//
// feature is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so the loader resolve reaches
// the host loader legs. The out-of-process CliMain path has no reverse channel, so it errors.

const featureUsage = `usage: charly feature <list [kind] | pending [entity] | validate [entity]>`

// runFeatureCLI dispatches the feature subcommand and formats the enumerated plan data the plugin's
// OWN enumeration returns (the plugin owns list/pending/validate output AND the load).
func runFeatureCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", featureUsage)
	}
	sub, rest := args[0], args[1:]
	filter := ""
	if len(rest) > 0 {
		filter = rest[0]
	}
	switch sub {
	case "-h", "--help", "help":
		fmt.Println(featureUsage)
		return nil
	case "list", "pending", "validate":
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		ents, err := enumerateFeatures(ctx, exec, dir, filter)
		if err != nil {
			return err
		}
		switch sub {
		case "list":
			for _, e := range ents {
				steps := planSteps(e.Plan)
				if e.Description == "" && len(steps) == 0 {
					fmt.Printf("%s %s: (no description)\n", e.Kind, e.Name)
					continue
				}
				nChecks := 0
				for _, s := range steps {
					if s.IsCheck {
						nChecks++
					}
				}
				fmt.Printf("%s %s: %q (%d step%s, %d check%s)\n",
					e.Kind, e.Name, summary(e.Description), len(steps), plural(len(steps)), nChecks, plural(nChecks))
			}
		case "pending":
			for _, e := range ents {
				for _, s := range planSteps(e.Plan) {
					if s.IsAgent {
						fmt.Printf("%s:%s — step %d: %s %q (agent-graded)\n", e.Kind, e.Name, s.Index, s.Keyword, s.Text)
					}
				}
			}
		case "validate":
			var errs []string
			for _, e := range ents {
				if e.Description == "" && len(e.Plan) == 0 {
					continue
				}
				errs = append(errs, kit.ValidatePlanSteps(e.Description, e.Plan, e.Kind+":"+e.Name)...)
			}
			if len(errs) > 0 {
				for _, er := range errs {
					fmt.Fprintln(os.Stderr, er)
				}
				return fmt.Errorf("%d validation error(s)", len(errs))
			}
			fmt.Println("All plan blocks validated successfully.")
		}
	default:
		return fmt.Errorf("unknown feature subcommand %q\n%s", sub, featureUsage)
	}
	return nil
}

// enumerateFeatures loads the project PLUGIN-SIDE (LoadUnifiedViaExecutor over the reverse channel —
// the former "feature" HostBuild seam's body, relocated K-wave 2 cone R6) and flattens every kind:
// entity into plain spec.FeatureEntity RAW data (description + plan, untransformed). Content-less
// candy layers are listed with empty data (the plugin renders them as "(no description)"); content-less
// box images are omitted (matching the former engine). exec nil (out-of-process CliMain, no reverse
// channel) → a clear error.
func enumerateFeatures(ctx context.Context, exec *sdk.Executor, dir, filter string) ([]spec.FeatureEntity, error) {
	if exec == nil {
		return nil, fmt.Errorf("charly feature requires compiled-in placement (the loader reverse channel is unavailable out-of-process)")
	}
	uf, present, err := loaderkit.LoadUnifiedViaExecutor(ctx, exec, dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present || uf == nil {
		return nil, fmt.Errorf("no charly.yml found in %s (run `charly box new project .` to scaffold one)", dir)
	}
	scanned, err := loaderkit.ProjectCandiesScanned(uf, dir, candyParseDoc(ctx, exec))
	if err != nil {
		return nil, err
	}
	return flattenFeatures(uf.ProjectConfig(), loaderkit.FinalizeScannedCandies(scanned, nil), filter), nil
}

// candyParseDoc is the per-candy-manifest parse seam the candy scan takes, bound to the registry
// snapshot over the EXISTING "loader-threaded" host leg + the build vocabulary. The feature command
// runs with the zero vocabulary (RegisterBuildVocabulary is only reached by the deploy path), so
// spec.NewCandyVocab(nil) matches the former host path's bare-command behavior exactly.
func candyParseDoc(ctx context.Context, exec *sdk.Executor) func(string) (*spec.CandyYAML, error) {
	threaded := loaderkit.LoaderThreadedViaExecutor(ctx, exec)
	return func(path string) (*spec.CandyYAML, error) {
		return loaderkit.ParseCandyManifest(path, threaded, spec.NewCandyVocab(nil))
	}
}

// flattenFeatures flattens the loaded project's candy readers + box config into raw
// spec.FeatureEntity data, applying the filter (empty | a kind "candy"/"box" | an entity id
// "candy:redis"). Split from enumerateFeatures so a unit test drives the flatten against synthetic
// candy-reader + config data without an executor.
func flattenFeatures(cfg *spec.Config, layers map[string]spec.CandyReader, filter string) []spec.FeatureEntity {
	f := strings.ToLower(strings.TrimSpace(filter))
	var ents []spec.FeatureEntity
	add := func(kind, name, desc string, plan []spec.Step) {
		eid := kind + ":" + name
		if f != "" && f != eid && f != kind {
			return
		}
		ents = append(ents, spec.FeatureEntity{Kind: kind, Name: name, Description: desc, Plan: plan})
	}
	for name, layer := range layers {
		if layer != nil {
			add("candy", name, layer.GetDescription(), layer.PlanSteps())
		}
	}
	for _, name := range cfg.AllBoxNames() {
		img, _ := cfg.BoxConfig(name)
		if img.Description != "" || len(img.Plan) > 0 {
			add("box", name, img.Description, img.Plan)
		}
	}
	return ents
}

// plural returns the plural suffix for a count (matches the former in-core summarizeDesc).
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// stepSummary is one plan step flattened for list/pending output — the plugin's OWN transform of the
// raw spec.Step the enumeration ships (formerly computed host-side as spec.FeatureStep; K3 moved the
// transform here since kit.KeywordOf/Step.KeywordText/Step.IsAgent are sdk-portable).
type stepSummary struct {
	Index   int
	Keyword string
	Text    string
	IsAgent bool
	IsCheck bool
}

// planSteps flattens a raw plan into stepSummary (the former host-side FeatureStep loop, moved here).
func planSteps(plan []spec.Step) []stepSummary {
	out := make([]stepSummary, len(plan))
	for i := range plan {
		step := plan[i]
		out[i] = stepSummary{
			Index:   i,
			Keyword: string(kit.KeywordOf(&step)),
			Text:    step.KeywordText(),
			IsAgent: step.IsAgent(),
			IsCheck: step.Check != "" || step.AgentCheck != "",
		}
	}
	return out
}

// summary renders a description's info line, or "(empty)" for a description-less entity with a plan
// (the former host-side summarizeDesc/DescriptionInfo call, moved here).
func summary(desc string) string {
	if s := deploykit.DescriptionInfo(desc); s != "" {
		return s
	}
	return "(empty)"
}
