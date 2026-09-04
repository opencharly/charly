package main

// cache_cmd.go — `charly cache status|clear|refresh` + the `--no-git-cache` bypass.
// The git-ref cache (spec/refs.GitClient) lives in the per-host config's `cache:`
// section; these verbs expose the operator surface: inspect, drop, or refresh it.

import (
	"fmt"
)

type CacheCmd struct {
	Status  CacheStatusCmd  `cmd:"" help:"Show the git-ref cache (path, entry count)"`
	Clear   CacheClearCmd   `cmd:"" help:"Drop every cached git answer — the next resolutions are fresh"`
	Refresh CacheRefreshCmd `cmd:"" help:"Drop the cache and note the next resolution re-warms the refs"`
}

type CacheStatusCmd struct{}

func (c CacheStatusCmd) Run() error {
	path, entries := gitClient().CacheStatus()
	fmt.Printf("cache: %s\nentries: %d (latest tags + default branches + resolved SHAs + downloads)\n", path, entries)
	return nil
}

type CacheClearCmd struct{}

func (c CacheClearCmd) Run() error {
	if err := gitClient().ClearCache(); err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	fmt.Println("git-ref cache cleared — the next resolution is fresh")
	return nil
}

type CacheRefreshCmd struct{}

func (c CacheRefreshCmd) Run() error {
	if err := gitClient().ClearCache(); err != nil {
		return fmt.Errorf("refresh cache: %w", err)
	}
	// The spec/refs WarmUp prefetches latest tags + default branches for the
	// project's repo set on the next project resolution (first-startup warm-up).
	fmt.Println("git-ref cache cleared — the next project resolution re-warms the refs")
	return nil
}

// noGitCache is set from the global `--no-git-cache` flag AFTER kong.Parse; the
// core gitClient() then bypasses every cached lookup (a forced-fresh resolution).
var noGitCache bool
