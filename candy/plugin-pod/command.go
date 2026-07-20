package pod

import (
	"fmt"

	"github.com/opencharly/sdk"
)

// command.go — the pod command-word DISPATCH. Each of charly's pod-lifecycle CLI words
// (start/stop/restart/config/shell/service/logs/remove/cp/volume) is an INDEPENDENT top-level
// grammar (unlike `charly bundle …`'s single grouped command), so dispatchPodCommand switches on
// the reserved word and kong-parses the pass-through args directly into the matching Cmd struct
// (mirroring candy/plugin-authoring's dispatchAuthoringCommand).

// dispatchPodCommand kong-parses args into the Cmd struct matching word and runs its leaf.
func dispatchPodCommand(word string, args []string) error {
	switch word {
	case "start":
		return sdk.RunInProcCLI("start", &StartCmd{}, args)
	case "stop":
		return sdk.RunInProcCLI("stop", &StopCmd{}, args)
	case "restart":
		return sdk.RunInProcCLI("restart", &RestartCmd{}, args)
	case "logs":
		return sdk.RunInProcCLI("logs", &LogsCmd{}, args)
	case "remove":
		return sdk.RunInProcCLI("remove", &RemoveCmd{}, args)
	case "shell":
		return sdk.RunInProcCLI("shell", &ShellCmd{}, args)
	default:
		return fmt.Errorf("pod: unsupported command word %q", word)
	}
}
