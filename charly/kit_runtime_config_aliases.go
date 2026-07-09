package main

// kit_runtime_config_aliases.go — package-main bindings onto the runtime-config
// STORE (RuntimeConfig types + Load/Save/Resolve + resolvers), moved to sdk/kit in
// P4. The `charly config` get/set/reset/list CLI (runtime_config_values.go) stays
// charly (credential/dotenv-coupled) and calls INTO this store.

import "github.com/opencharly/sdk/kit"

type (
	RuntimeConfig   = kit.RuntimeConfig
	RuntimeVmConfig = kit.RuntimeVmConfig
	EngineConfig    = kit.EngineConfig
	ResolvedRuntime = kit.ResolvedRuntime
)

var (
	LoadRuntimeConfig           = kit.LoadRuntimeConfig
	SaveRuntimeConfig           = kit.SaveRuntimeConfig
	ResolveRuntime              = kit.ResolveRuntime
	ResolveValue                = kit.ResolveValue
	SystemdUserAvailable        = kit.SystemdUserAvailable
	SystemdUserRuntimeDir       = kit.SystemdUserRuntimeDir
	expandHostHome              = kit.ExpandHostHome
	detectRunMode               = kit.DetectRunMode
	resolveEncryptedStoragePath = kit.ResolveEncryptedStoragePath
	resolveVolumesPath          = kit.ResolveVolumesPath
	RuntimeConfigPath           = kit.RuntimeConfigPath
	ValidateEngine              = kit.ValidateEngine
	ValidateRunMode             = kit.ValidateRunMode
	ValidateBindAddress         = kit.ValidateBindAddress
)
