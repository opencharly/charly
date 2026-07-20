package main

import (
	pi "github.com/opencharly/charly/candy/plugin-agent-pi"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(pi.NewProvider(), pi.NewMeta()) }
