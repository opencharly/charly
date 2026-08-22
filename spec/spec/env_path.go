package spec

import "strings"

// ExpandPath expands ~, ${HOME} and $HOME in a path string to the given home
// directory. ${HOME} is replaced before bare $HOME so the braced form is
// handled (a bare $HOME ReplaceAll would not match "${HOME}").
//
// Relocated from sdk/kit/env.go (#55 import-purity cone-render): a pure fabric
// path helper with no host dependency. kit.ExpandPath now aliases spec.ExpandPath
// (R3, one source), and spec.BuildServiceRenderContext consumes it in-package.
func ExpandPath(path string, home string) string {
	// Expand ~ at the start of the path
	if strings.HasPrefix(path, "~/") {
		path = home + path[1:]
	} else if path == "~" {
		path = home
	}

	// Expand ${HOME} then $HOME anywhere in the path
	path = strings.ReplaceAll(path, "${HOME}", home)
	path = strings.ReplaceAll(path, "$HOME", home)

	return path
}
