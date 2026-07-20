// Command serve is the out-of-process placement of the same typed terminal
// provider that can be compiled into charly. It has no shell-back CLI mode.
package main

import (
	tmux "github.com/opencharly/charly/candy/plugin-tmux"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(tmux.NewProvider(), tmux.NewMeta()) }
