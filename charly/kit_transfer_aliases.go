package main

// kit_transfer_aliases.go — bindings onto the image-transfer helpers (transfer.go)
// moved to sdk/kit in P4. EnsureImage + loadProjectCfgFromCwd (Config-coupled) stay
// in charly/transfer.go.

import "github.com/opencharly/sdk/kit"

var (
	LocalImageExists  = kit.LocalImageExists
	TransferImage     = kit.TransferImage
	TransferToRootful = kit.TransferToRootful
)
