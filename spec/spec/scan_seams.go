package spec

// scan_seams.go — the host-coupled legs the candy-scan fetch fix-point (loaderkit.ScanCandyFromLocal)
// reaches. Relocated from sdk/loaderkit/scan_orchestrate.go (#55 C3b-ii) so it can be the parameter
// type on the spec.ProjectLoader.ScanCandyFromLocal seam method — the interface lives in this
// dedicated spec module, so its param types must live here too. The caller (charly's scanSeamsFor,
// candy/plugin-build's scanSeamsLeg) builds these as closures capturing its config/opts + host
// mechanisms (registry, refs backend); the pure fix-point in loaderkit never inspects a
// package-main type. loaderkit keeps a `type ScanSeams = spec.ScanSeams` forwarder (mirroring its
// RemoteDownload alias) so its own signature + candy/plugin-build's call sites stay terse.
type ScanSeams struct {
	// CollectRemoteRefs runs the reachability-scoped remote-ref walk over the project's boxes +
	// this local candy set, returning each distinct (repo, git-tag) to fetch. Host closure:
	// CollectRemoteRefsOpts(cfg, FinalizeScannedCandies(localScanned, nil), WithLocalRawRefs(opts, localScanned)).
	CollectRemoteRefs func(localScanned map[string]ScannedCandy) ([]RemoteDownload, error)
	// EnsureRepo resolves a (repoPath, version) to a local cache directory, fetching + auto-migrating
	// on a cache miss (host closure: EnsureRepoDownloaded).
	EnsureRepo func(repoPath, version string) (string, error)
	// ScanRemote scans the wanted bare refs out of a downloaded repo cache dir (host closure:
	// requireCandyScanner().ScanRemoteCandy(cacheDir, repoPath, wantRefs, parseCandyYAML)).
	ScanRemote func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]ScannedCandy, error)
}
