package secrets

import (
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
)

// aliases.go reuses (does NOT copy — R3) the two stdlib-light, plugin-importable charly
// utility packages the externalized GPG `.secrets` surface needs, ported alongside
// secrets_gpg.go out of charly's core:
//   - kit.ShellQuote — the canonical POSIX single-quoter (the same one core references
//     directly), used by `secrets gpg env` to emit safe `export KEY='value'` lines.
//   - vmshared.{Register,Unregister}TempCleanup — the temp-file kill-survivability
//     registry (charly-secrets-* temps from `secrets gpg edit`/`decrypt` are in
//     vmshared.sweepablePatterns, so a later `charly` invocation's SweepStaleTemps reaps
//     a leftover even after SIGKILL); cliMain arms the in-process signal handler.
var (
	shellQuote            = kit.ShellQuote
	RegisterTempCleanup   = vmshared.RegisterTempCleanup
	UnregisterTempCleanup = vmshared.UnregisterTempCleanup
)
