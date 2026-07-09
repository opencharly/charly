package main

// kit_http_fetch_aliases.go — bindings onto the qcow2 fetch helper (http_fetch.go)
// moved to sdk/kit in P4.

import "github.com/opencharly/sdk/kit"

type FetchedImage = kit.FetchedImage

var FetchQcow2 = kit.FetchQcow2
var acquireLocalPkgBuildLock = kit.AcquireLocalPkgBuildLock
