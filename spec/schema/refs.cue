// refs.cue — the remote-repo FETCH-BACKEND wire types shared between the loader plugin
// (candy/plugin-loader, which owns the fetch ORCHESTRATION since K-wave 2 cone R1) and the swappable
// backend that serves it (candy/plugin-refs by default). They live in package spec — the ONE
// importable home — because BOTH sides construct and exchange them across an InvokeProvider
// boundary, and neither may import the other.
//
// Why these exist at all: the backend used to be reached through a TYPED in-proc handle
// (spec.RefsDownloader, resolved host-side into spec.RefsCollectSeams.Downloader and threaded down
// into the relocated mechanism). That worked only while charly core sat in the middle of every
// fetch. Once the loader plugin builds its own seams, it needs to reach the refs provider the way
// any plugin reaches any peer — InvokeProvider(class:"refs", word:"refs", OpResolve) — so the
// (repoPath, version) -> cacheDir call needs a wire shape. Plain structs; gengotypes generates them
// faithfully. The typed spec.RefsDownloader interface survives for the in-proc registration seam
// itself; this is its wire face.

// #RefsDownloadInput is the cache-MISS download request the loader plugin ships to the registered
// refs backend. The caller has already resolved local overrides and checked the cache, exactly as
// the pre-move host orchestration did — a backend only ever sees a genuine miss.
#RefsDownloadInput: {
	// repo_path is the bare repo coordinate (e.g. "github.com/opencharly/charly").
	repo_path!: string @go(RepoPath)
	// version is the git tag / branch / sha to fetch. Never empty at this point: an empty authored
	// version is resolved to the repo's default branch before the request is built.
	version!: string @go(Version)
}

// #RefsDownloadReply carries the populated local cache tree the backend produced.
#RefsDownloadReply: {
	// dir is the absolute path of the populated cache directory.
	dir?: string @go(Dir)
}
