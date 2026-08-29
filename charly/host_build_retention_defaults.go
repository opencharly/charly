package main

// host_build_retention_defaults.go — the "retention-defaults" host-builder: a
// LIGHTWEIGHT read of the project's defaults.keep_images / defaults.keep_check_runs
// that does NOT walk the project or resolve its @github refs.
//
// Why it exists: the clean command (candy/plugin-clean) needs the retention tunables
// but previously reached them via loaderkit.ResolveRetentionDefaultsViaExecutor →
// LoadUnifiedViaExecutor, which WALKS the project — resolving every version-less
// @github import ref with a `git ls-remote` freshness check (70 network calls on the
// charly repo's own project, measured). `charly clean --dry-run` therefore hung past
// the 2m never-hang ceiling (issue #423). The clean command does not need the candies
// or the refs — only the two defaults — so this seam reads the charly.yml defaults
// directly, with zero walk and zero network.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

const retentionDefaultsBuilderKind = "retention-defaults"

// retentionDefaultsYAML is the minimal charly.yml shape this seam reads — only the
// defaults.keep_images / defaults.keep_check_runs fields. Everything else is ignored.
type retentionDefaultsYAML struct {
	Defaults struct {
		KeepImages     *int `yaml:"keep_images"`
		KeepCheckRuns *int `yaml:"keep_check_runs"`
	} `yaml:"defaults"`
}

// hostBuildRetentionDefaults reads the project's retention defaults from charly.yml
// WITHOUT walking the project or resolving its @github refs. An absent / unreadable
// charly.yml degrades to 0/0 (retention disabled), matching the deleted seam's
// best-effort contract.
func hostBuildRetentionDefaults(_ context.Context, req spec.RetentionRequest, _ buildEngineContext) (spec.RetentionReply, error) {
	dir := req.Dir
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, spec.UnifiedFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return spec.RetentionReply{}, nil // absent project → retention disabled
	}
	var cfg retentionDefaultsYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return spec.RetentionReply{}, nil // unparseable → retention disabled
	}
	reply := spec.RetentionReply{}
	if cfg.Defaults.KeepImages != nil {
		reply.KeepImages = *cfg.Defaults.KeepImages
	}
	if cfg.Defaults.KeepCheckRuns != nil {
		reply.KeepCheckRuns = *cfg.Defaults.KeepCheckRuns
	}
	return reply, nil
}

func init() {
	registerHostBuilder(retentionDefaultsBuilderKind, typedHostBuilder(retentionDefaultsBuilderKind, hostBuildRetentionDefaults))
}
