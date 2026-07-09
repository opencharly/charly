package main

import "github.com/opencharly/sdk/deploykit"

// CandyModel — the read-only candy interface the deploy-plan compiler reads a candy
// through. charly's runtime Candy implements it (candy_model_accessors.go).
type CandyModel = deploykit.CandyModel

// Compiler helper types/vars moved to deploykit with install_build.go (P4); charly
// binds onto them (builder_preresolve.go builds BuilderPreresolved; shell code reads
// ShellAllowlist).
type builderPreresolved = deploykit.BuilderPreresolved

var ShellAllowlist = deploykit.ShellAllowlist

// Op-context enum moved to deploykit with the compiler; charly binds onto it and
// injects the VerbCatalog-coupled classifier as the OpInContext seam.
type ExecContext = deploykit.ExecContext

const (
	CtxBuild   = deploykit.CtxBuild
	CtxDeploy  = deploykit.CtxDeploy
	CtxRuntime = deploykit.CtxRuntime
)

func init() { deploykit.OpInContext = opInContext }

var builderCtxKey = deploykit.BuilderCtxKey
