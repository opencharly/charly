package main

// kit_env_aliases.go — package-main bindings onto the env-config helpers (env.go)
// moved to sdk/kit in P4 (the pure-helpers batch).

import "github.com/opencharly/sdk/kit"

type EnvConfig = kit.EnvConfig

var (
	envMapToPairs   = kit.EnvMapToPairs
	envPairsToMap   = kit.EnvPairsToMap
	ExpandEnvConfig = kit.ExpandEnvConfig
	MergeEnvConfigs = kit.MergeEnvConfigs
	ExpandPath      = kit.ExpandPath
)
