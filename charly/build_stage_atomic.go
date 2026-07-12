package main

// build_stage_atomic.go — race-free, deterministic .build/ staging primitives.
//
// The .build/ tree is SHARED by every concurrent `charly box build` /
// `charly box generate` in one project dir (the _candy staging dirs + each
// image's Containerfile). To let parallel beds fan out without a per-dir build
// lock (serializing cold builds is catastrophic for wall-clock), every shared
// write is made ATOMIC + IDEMPOTENT instead: a concurrent reader (a podman build
// COPYing from _candy, or reading a Containerfile) always sees a COMPLETE,
// deterministic artifact — never a half-removed dir (the `directory not empty` /
// `no such file` race) or a partially-written file. Identical inputs always
// produce identical bytes, so podman's content+instruction-keyed cache hits.

import (
	"github.com/opencharly/sdk/kit"
)

// atomicWriteFile → kit.AtomicWriteFile (P8 shim — the pure atomic-write primitive
// moved to sdk/kit so the build render engine shares it).
var atomicWriteFile = kit.AtomicWriteFile

// installDirAtomic → kit.InstallDirAtomic (P8b shim — the pure atomic dir-install
// primitive (renameat2 RENAME_EXCHANGE) moved to sdk/kit so the build render engine
// and charly's staging primitives share the one primitive).
var installDirAtomic = kit.InstallDirAtomic
