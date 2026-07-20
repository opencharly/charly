package agentkind

import (
	"fmt"
	"os"
	"os/exec"
)

// TuiCmd is deliberately only a process-placement shim. The installed Pi-TUI
// client calls typed `charly agent` leaves and owns no SSH, tmux, container,
// runtime, persistence, or orchestration behavior.
type TuiCmd struct {
	Binary string `long:"binary" env:"CHARLY_TUI_BIN" default:"charly-agent-tui" help:"Pi-TUI client executable"`
}

func (c *TuiCmd) Run() error {
	path, err := exec.LookPath(c.Binary)
	if err != nil {
		return fmt.Errorf("find Pi-TUI client %q: %w", c.Binary, err)
	}
	charly, err := charlyBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "CHARLY_BIN="+charly)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
