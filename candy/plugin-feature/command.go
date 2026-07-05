package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// command.go — the externalized `charly feature` command (list / pending / validate — inspect
// plan-shaped descriptions). The plugin OWNS the subcommand grammar + the output formatting; the
// genuine core subsystem it can't hold — the unified LOADER (LoadConfig / ScanCandy — the kernel), the
// Step plan model, and validatePlanSteps (shared with `charly box validate`, R3) — stays core and is
// reached via the generic "feature" HostBuild seam, which enumerates every entity's plan into plain
// DATA (charly/host_build_feature.go). No hidden `__feature-*` forward.
//
// (The Feature RUN verbs — `charly box feature run` / `charly check feature run` — stay children of
// box/check in the core binary, NOT part of this plugin.)
//
// feature is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so HostBuild("feature") reaches
// the host loader. The out-of-process CliMain path has no reverse channel, so it errors.

const featureUsage = `usage: charly feature <list [kind] | pending [entity] | validate [entity]>`

// runFeatureCLI dispatches the feature subcommand and formats the enumerated plan data the "feature"
// HostBuild seam returns (the plugin owns list/pending/validate output; the loader stays core).
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
		reply, err := hostFeature(ctx, exec, spec.FeatureRequest{Filter: filter})
		if err != nil {
			return err
		}
		switch sub {
		case "list":
			for _, e := range reply.Entities {
				if e.Description == "" && len(e.Steps) == 0 {
					fmt.Printf("%s %s: (no description)\n", e.Kind, e.Name)
					continue
				}
				nChecks := 0
				for _, s := range e.Steps {
					if s.IsCheck {
						nChecks++
					}
				}
				fmt.Printf("%s %s: %q (%d step%s, %d check%s)\n",
					e.Kind, e.Name, e.Summary, len(e.Steps), plural(len(e.Steps)), nChecks, plural(nChecks))
			}
		case "pending":
			for _, e := range reply.Entities {
				for _, s := range e.Steps {
					if s.IsAgent {
						fmt.Printf("%s:%s — step %d: %s %q (agent-graded)\n", e.Kind, e.Name, s.Index, s.Keyword, s.Text)
					}
				}
			}
		case "validate":
			var errs []string
			for _, e := range reply.Entities {
				errs = append(errs, e.ValidationErrors...)
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

// plural returns the plural suffix for a count (matches the former in-core summarizeDesc).
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// hostFeature enumerates the project's plans over the generic "feature" HostBuild kind. exec is nil on
// the out-of-process cliMain path (no reverse channel) → a clear error.
func hostFeature(ctx context.Context, exec *sdk.Executor, req spec.FeatureRequest) (spec.FeatureReply, error) {
	if exec == nil {
		return spec.FeatureReply{}, fmt.Errorf("charly feature requires compiled-in placement (the feature host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.FeatureReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "feature", reqJSON)
	if err != nil {
		return spec.FeatureReply{}, err
	}
	var reply spec.FeatureReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return spec.FeatureReply{}, uerr
	}
	if reply.Error != "" {
		return spec.FeatureReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}
