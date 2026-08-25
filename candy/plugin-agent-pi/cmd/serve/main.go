package main

import (
	pi "github.com/opencharly/plugin-agent-pi/candy/plugin-agent-pi"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(pi.NewProvider(), pi.NewMeta()) }
