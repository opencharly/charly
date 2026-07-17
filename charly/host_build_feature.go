package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// host_build_feature.go — the generic "feature" F10 host-builder. The externalized `charly feature`
// command plugin (candy/plugin-feature) OWNS the list/pending/validate grammar + output, INCLUDING
// the plan-to-summary transform (kit.KeywordOf/kit.ValidatePlanSteps/deploykit.DescriptionInfo are
// sdk-portable, K3) and asks the host to enumerate the project's RAW plans via
// Executor.HostBuild("feature", spec.FeatureRequest{...}). The unified LOADER (LoadConfig / ScanCandy
// — the kernel) STAYS core, reached via this generic action noun (F11) — this file needs no
// kit/deploykit import as a result of the transform moving to the plugin. The seam returns plain
// enumerated RAW data (description + plan) — the plugin does all the summarizing/validating/
// formatting/verdict/exit-code logic.
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

// enumerateFeatures loads the project config + candies and flattens every kind: entity into plain
// spec.FeatureEntity RAW data (description + plan, untransformed). Content-less candy layers are
// listed with empty data (the plugin renders them as "(no description)"); content-less box images are
// omitted (matching the former engine). Split from the host-builder so a unit test can drive the real
// loader against a fixture.
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
		ents = append(ents, spec.FeatureEntity{Kind: kind, Name: name, Description: desc, Plan: plan})
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
