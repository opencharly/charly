package main

import "github.com/opencharly/sdk/kit"

// kit_refs_aliases.go — package-main bindings onto the remote-repo git primitives + cache-path
// helpers that live ONCE in sdk/kit (P7), shared with candy/plugin-refs (the fetch backend) and
// core (reconcile's --remote query, the collection walk, bed setup). These thin aliases keep
// core's call sites unchanged after refs_git.go + the cache-path helpers moved to kit.
var (
	RepoGitURL       = kit.RepoGitURL
	GitClone         = kit.GitClone
	GitDefaultBranch = kit.GitDefaultBranch
	GitLatestTag     = kit.GitLatestTag
	compareSemver    = kit.CompareSemver
	RepoCacheDir     = kit.RepoCacheDir
	RepoCachePath    = kit.RepoCachePath
	IsRepoCached     = kit.IsRepoCached
)
