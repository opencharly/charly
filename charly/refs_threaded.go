package main

import (
	"log"

	"github.com/opencharly/spec/spec"
)

// refs_threaded.go — the host's registered remote-repo FETCH BACKEND (P7). activeRefsDownloader is
// the spec.RefsDownloader of the compiled-in refs plugin (candy/plugin-refs), wired at registration
// (plugin_inproc.go) when a provider implementing spec.RefsDownloader registers. There is NO in-core
// fallback (#55 dropped the kit.DefaultDownloader{} bootstrap default — the DefaultDownloader git
// backend is candy/plugin-refs's capability, not core's): the compiled-in refs plugin registers at
// init before the first load (Go runs plugins_generated.go's init() before main(), so before any
// @github resolution can call EnsureRepoDownloaded), so a nil downloader means candy/plugin-refs was
// not compiled in — a FATAL, never a silent fallback. EnsureRepoDownloaded dispatches every
// cache-miss download through requireRefsDownloader(); swapping the refs plugin swaps the fetch
// backend (git → OCI/S3). Mirrors activeLoaderParser (loader_threaded.go, P6).
var activeRefsDownloader spec.RefsDownloader

// requireRefsDownloader returns the registered fetch backend or FATALs with a clear message, so a
// binary built without candy/plugin-refs fails loudly and identically at every download site.
func requireRefsDownloader() spec.RefsDownloader {
	if activeRefsDownloader == nil {
		log.Fatal("no refs plugin registered — charly was built without candy/plugin-refs (the remote-repo fetch backend)")
	}
	return activeRefsDownloader
}
