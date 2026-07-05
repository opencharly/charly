package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// host_build_feature.go — the generic "feature" F10 host-builder. The externalized `charly feature`
// command plugin (candy/plugin-feature) OWNS the list/pending/validate grammar + output and asks the
// host to enumerate the project's plans via Executor.HostBuild("feature", spec.FeatureRequest{...}).
// The unified LOADER (LoadConfig / ScanCandy — the kernel), the Step plan model, and validatePlanSteps
// (shared with `charly box validate`, R3) STAY core, reached via this generic action noun (F11). The
// seam returns plain enumerated DATA — the plugin does all the formatting/verdict/exit-code logic.
const featureBuilderKind = "feature"

func hostBuildFeature(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var req spec.FeatureRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return marshalJSON(spec.FeatureReply{Error: fmt.Sprintf("feature host-build: decode: %v", err)})
	}
	dir, err := os.Getwd()
	if err != nil {
		return marshalJSON(spec.FeatureReply{Error: err.Error()})
	}
	ents, err := enumerateFeatures(dir, req.Filter)
	if err != nil {
		return marshalJSON(spec.FeatureReply{Error: err.Error()})
	}
	return marshalJSON(spec.FeatureReply{Entities: ents})
}

// enumerateFeatures loads the project config + candies and flattens every kind: entity's plan into
// plain spec.FeatureEntity data (summary, per-step keyword/text/agent/check flags, and the shared
// validatePlanSteps errors). Content-less candy layers are listed with empty data (the plugin renders
// them as "(no description)"); content-less box images are omitted (matching the former engine). Split
// from the host-builder so a unit test can drive the real loader + plan model against a fixture.
func enumerateFeatures(dir, filter string) ([]spec.FeatureEntity, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return nil, fmt.Errorf("scanning candies: %w", err)
	}
	f := strings.ToLower(strings.TrimSpace(filter))

	var ents []spec.FeatureEntity
	add := func(kind, name, desc string, plan []Step) {
		eid := kind + ":" + name
		if f != "" && f != eid && f != kind {
			return
		}
		e := spec.FeatureEntity{Kind: kind, Name: name, Description: desc}
		if desc != "" || len(plan) > 0 {
			if s := descriptionInfo(desc); s != "" {
				e.Summary = s
			} else {
				e.Summary = "(empty)"
			}
			for i := range plan {
				step := plan[i]
				e.Steps = append(e.Steps, spec.FeatureStep{
					Index:   i,
					Keyword: string(keywordOf(&step)),
					Text:    step.KeywordText(),
					IsAgent: step.IsAgent(),
					IsCheck: step.Check != "" || step.AgentCheck != "",
				})
			}
			e.ValidationErrors = validatePlanSteps(desc, plan, eid)
		}
		ents = append(ents, e)
	}

	for name, layer := range layers {
		if layer != nil {
			add("candy", name, layer.Description, layer.plan)
		}
	}
	for name, img := range cfg.Box {
		if img.Description != "" || len(img.Plan) > 0 {
			add("box", name, img.Description, img.Plan)
		}
	}
	return ents, nil
}

var _ = func() bool { registerHostBuilder(featureBuilderKind, hostBuildFeature); return true }()
