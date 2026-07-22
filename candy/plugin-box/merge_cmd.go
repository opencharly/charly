package box

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// merge_cmd.go — the `charly box merge` handler (P14: relocated OUT of charly core;
// charly/merge.go + its share of charly/oci_plugin.go are deleted). It resolves a box (or every
// merge.auto box) from the generic resolved-project envelope, then reaches verb:oci DIRECTLY over
// InvokeProvider — the SAME F10 peer-dispatch leg candy/plugin-build's own post-build inline merge
// (mergeBox, drive.go) already uses, so this is the SECOND caller of an established pattern, not a
// new one (R3). The go-containerregistry merge engine itself has lived out-of-process in
// candy/plugin-oci since the P14a cutover; this plugin never imports go-containerregistry.

// mergeGrammar is the `charly box merge [box] [--all] [--max-mb] [--max-total-mb] [--tag]
// [--dry-run]` CLI surface — unchanged from the former core MergeCmd.
type mergeGrammar struct {
	Box        string `arg:"" optional:"" help:"Box name from charly.yml"`
	All        bool   `long:"all" help:"Merge all images with merge.auto enabled"`
	MaxMB      int    `long:"max-mb" help:"Maximum size of a merged layer (MB)"`
	MaxTotalMB int    `long:"max-total-mb" help:"Maximum total image size for merge (MB, 0=no limit)"`
	Tag        string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	DryRun     bool   `long:"dry-run" help:"Print merge plan without modifying the image"`
}

// mergeDefaultMaxMB / mergeDefaultMaxTotalMB are the CLI-resolution defaults (CLI flag → box
// config → default) — the SAME safety defaults candy/plugin-oci applies when a request arrives
// with 0 (kept in sync by convention, like the former core/plugin pair).
const mergeDefaultMaxMB = 128
const mergeDefaultMaxTotalMB = 0 // 0 = no limit

func dispatchMerge(hc *hostClient, args []string) error {
	var g mergeGrammar
	if done, err := parseLeaf("merge", &g, args); err != nil || done {
		return err
	}
	if g.Box == "" && !g.All {
		return fmt.Errorf("specify a box name or use --all")
	}
	rp, err := hc.resolvedProject(false)
	if err != nil {
		return err
	}
	if g.All {
		return mergeAllBoxes(hc, rp, g)
	}
	return mergeOneBox(hc, rp, g.Box, g)
}

// mergeAllBoxes merges every box with merge.auto enabled, in the host-resolved dependency order
// (rp.BuildTargets — the same GlobalCandyOrder-derived ordering the former core ResolveBoxOrder
// produced) so base images merge before their children.
func mergeAllBoxes(hc *hostClient, rp *spec.ResolvedProject, g mergeGrammar) error {
	merged := 0
	for _, target := range rp.BuildTargets {
		box, ok := rp.Boxes[target.Name]
		if !ok || box.Merge == nil || !box.Merge.Auto {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n--- %s ---\n", target.Name)
		if err := mergeOneBox(hc, rp, target.Name, g); err != nil {
			// Per-image merge failures are non-fatal, matching the former core behaviour.
			fmt.Fprintf(os.Stderr, "Warning: skipping merge for %s: %v\n", target.Name, err)
			continue
		}
		merged++
	}
	if merged == 0 {
		fmt.Fprintf(os.Stderr, "No images have merge.auto enabled\n")
	}
	return nil
}

// resolveMergeLimits applies the CLI flag -> box config -> default precedence for maxMB /
// maxTotalMB (pure, no I/O — split out of mergeOneBox so it unit-tests without a live executor).
func resolveMergeLimits(boxMerge *spec.BoxMerge, cliMaxMB, cliMaxTotalMB int) (maxMB, maxTotalMB int) {
	maxMB = mergeDefaultMaxMB
	if boxMerge != nil && boxMerge.MaxMB > 0 {
		maxMB = boxMerge.MaxMB
	}
	if cliMaxMB > 0 {
		maxMB = cliMaxMB
	}

	maxTotalMB = mergeDefaultMaxTotalMB
	if boxMerge != nil && boxMerge.MaxTotalMB > 0 {
		maxTotalMB = boxMerge.MaxTotalMB
	}
	if cliMaxTotalMB > 0 {
		maxTotalMB = cliMaxTotalMB
	}
	return maxMB, maxTotalMB
}

// mergeOneBox merges a single image: resolve its Registry/Tag/Merge settings from the envelope,
// then hand a spec.MergeRequest to verb:oci over InvokeProvider and print the reply's progress
// Notes — the exact former core runOne contract.
func mergeOneBox(hc *hostClient, rp *spec.ResolvedProject, boxName string, g mergeGrammar) error {
	box, ok := rp.Boxes[boxName]
	if !ok {
		return fmt.Errorf("box %q not found", boxName)
	}

	maxMB, maxTotalMB := resolveMergeLimits(box.Merge, g.MaxMB, g.MaxTotalMB)
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}

	reqJSON, err := json.Marshal(spec.MergeRequest{
		ImageRef:   kit.ResolveShellImageRef(box.Registry, box.Name, g.Tag),
		Engine:     rt.BuildEngine,
		MaxMB:      maxMB,
		MaxTotalMB: maxTotalMB,
		DryRun:     g.DryRun,
	})
	if err != nil {
		return err
	}
	envJSON, err := json.Marshal(map[string]string{"oci_op": "merge"})
	if err != nil {
		return err
	}
	replyJSON, err := hc.exec.InvokeProvider(hc.ctx, "verb", "oci", sdk.OpRun, reqJSON, envJSON, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.MergeReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return fmt.Errorf("box merge: decode reply: %w", err)
	}
	for _, note := range reply.Notes {
		fmt.Fprintln(os.Stderr, note)
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}
