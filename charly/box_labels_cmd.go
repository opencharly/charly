package main

import (
	"fmt"
	"sort"
	"strings"
)

// BoxLabelsCmd implements `charly box labels <ref>` — print a BUILT image
// ref's OCI labels (the ai.opencharly.* capability contract) straight from
// local container storage. This is the charly-native artifact-label probe
// behind CLAUDE.md R8: an empty or missing capability label is a FAILURE,
// and probing it never needs an ad-hoc `podman inspect`.
type BoxLabelsCmd struct {
	Image  string `arg:"" help:"Image reference (full ref or short name resolved against local container storage; never reads charly.yml)"`
	Format string `name:"format" help:"Print only this label's raw value — a full key, or the ai.opencharly.<key> shorthand (e.g. 'init'); exits non-zero when the label is absent"`
	All    bool   `name:"all" help:"Print every label, not just the ai.opencharly.* contract"`
}

func (c *BoxLabelsCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	imageRef, err := resolveLocalImageRef(rt.RunEngine, c.Image)
	if err != nil {
		return err
	}
	labels, err := InspectLabels(rt.RunEngine, imageRef)
	if err != nil {
		if !LocalImageExists(rt.RunEngine, imageRef) {
			return fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
		}
		return err
	}
	if c.Format != "" {
		key := canonicalLabelKey(c.Format)
		v, ok := labels[key]
		if !ok {
			return fmt.Errorf("label %q not present on %s — an empty or missing capability label is a failure (CLAUDE.md R8)", key, imageRef)
		}
		fmt.Println(v)
		return nil
	}
	keys := sortedLabelKeys(labels, c.All)
	if len(keys) == 0 {
		return fmt.Errorf("no %s labels on %s — not an opencharly image (use --all for every label)", "ai.opencharly.*", imageRef)
	}
	for _, k := range keys {
		fmt.Printf("%s=%s\n", k, labels[k])
	}
	return nil
}

// canonicalLabelKey expands the ai.opencharly.<key> shorthand: a bare token
// without dots refers to the capability-contract namespace.
func canonicalLabelKey(k string) string {
	if strings.Contains(k, ".") {
		return k
	}
	return "ai.opencharly." + k
}

// sortedLabelKeys returns the label keys to print, sorted; without --all only
// the ai.opencharly.* contract participates.
func sortedLabelKeys(labels map[string]string, all bool) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if !all && !strings.HasPrefix(k, "ai.opencharly.") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
