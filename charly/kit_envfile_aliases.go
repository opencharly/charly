package main

// kit_envfile_aliases.go — bindings onto the env-file/dotenv helpers (envfile.go)
// moved to sdk/kit in P4.

import "github.com/opencharly/sdk/kit"

var (
	DotenvLoaded      = kit.DotenvLoaded
	LoadProcessDotenv = kit.LoadProcessDotenv
	ParseEnvFile      = kit.ParseEnvFile
	ParseEnvBytes     = kit.ParseEnvBytes
	LoadWorkspaceEnv  = kit.LoadWorkspaceEnv
	ResolveEnvVars    = kit.ResolveEnvVars
	enrichNoProxy     = kit.EnrichNoProxy
)
