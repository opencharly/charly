package spec

// RemoteDownload represents a unique (repo, version) pair to download in the remote-layer resolver's
// candy-scan fix-point, plus the bare refs to import from it. A pure data descriptor over strings,
// relocated to the dedicated spec module (#55 2b Class A) so charly's remote-resolver files (refs.go)
// reach it without importing loaderkit; loaderkit aliases it for the scan mechanism that produces it.
type RemoteDownload struct {
	RepoPath string
	Version  string
	Refs     []string // bare refs to import (e.g. "github.com/org/repo/candy/name")
}
