package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_feature.go — the generic "feature" F10 host-builder. The externalized `charly feature`
// command plugin (candy/plugin-feature) OWNS the list/pending/validate grammar + output and asks the
// host to enumerate the project's plans via Executor.HostBuild("feature", spec.FeatureRequest{...}).
// The unified LOADER (LoadConfig / ScanCandy — the kernel) and the Step plan model STAY core, reached
// via this generic action noun (F11). kit.ValidatePlanSteps (P12a: relocated to sdk/kit — it is invoked
// by BOTH this seam AND `charly box validate`, so it lives where both reach it without a core→plugin
// import, R3) is called here, not defined here. The seam returns plain enumerated DATA — the plugin
// does all the formatting/verdict/exit-code logic.
const featureBuilderKind = "feature"

func hostBuildFeature(_ context.Context, req spec.FeatureRequest, _ buildEngineContext) (spec.FeatureReply, error) {
	dir, err := os.Getwd()
	if err != nil {
		return spec.FeatureReply{Error: err.Error()}, nil
	}
	ents, err := enumerateFeatures(dir, req.Filter)
	if err != nil {
		return spec.FeatureReply{Error: err.Error()}, nil
	}
	return spec.FeatureReply{Entities: ents}, nil
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
	add := func(kind, name, desc string, plan []spec.Step) {
		eid := kind + ":" + name
		if f != "" && f != eid && f != kind {
			return
		}
		e := spec.FeatureEntity{Kind: kind, Name: name, Description: desc}
		if desc != "" || len(plan) > 0 {
			if s := deploykit.DescriptionInfo(desc); s != "" {
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
			e.ValidationErrors = kit.ValidatePlanSteps(desc, plan, eid)
		}
		ents = append(ents, e)
	}

	for name, layer := range layers {
		if layer != nil {
			add("candy", name, layer.Description, layer.plan)
		}
	}
	for _, name := range cfg.allBoxNames() {
		img, _ := cfg.BoxConfig(name)
		if img.Description != "" || len(img.Plan) > 0 {
			add("box", name, img.Description, img.Plan)
		}
	}
	return ents, nil
}

var _ = func() bool {
	registerHostBuilder(featureBuilderKind, typedHostBuilder(featureBuilderKind, hostBuildFeature))
	return true
}()
