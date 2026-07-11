package main

import "github.com/opencharly/sdk/kit"

// refs_threaded.go — the host's registered remote-repo FETCH BACKEND (P7). activeRefsDownloader is
// the kit.RefsDownloader of the compiled-in refs plugin (candy/plugin-refs), wired at registration
// when a provider that implements kit.RefsDownloader registers (plugin_inproc.go). Defaults to the
// built-in git backend (kit.DefaultDownloader) so the bootstrap path (before any refs plugin
// registers — the compiled-in refs plugin registers at init before the first load) still fetches.
// EnsureRepoDownloaded dispatches every cache-miss download here; swapping the refs plugin swaps the
// fetch backend (git → OCI/S3). Mirrors activeLoaderParser (loader_threaded.go, P6).
var activeRefsDownloader kit.RefsDownloader = kit.DefaultDownloader{}
