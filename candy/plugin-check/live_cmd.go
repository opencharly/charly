package check

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// live_cmd.go — the `charly check live` leaf. It gathers no config itself: the full-stack live
// gathering (vm/pod/local/group classification, venue construction, OCI-label plan extraction,
// runtime-var resolution) runs HOST-side behind the generic "check-run" seam (Mode:"live"); the
// plugin sends the CLI inputs, prints the host-built kind-specific Header banner, and formats the
// returned []StepResult. The one non-plan-run live path — a nested pod-in-VM leaf whose check the
// host delegates to the guest over SSH (`charly check live <pod>` run INSIDE the guest) — comes
// back as reply.Passthrough, whose stdout/stderr + exit code the plugin forwards verbatim.

// CheckLiveCmd runs the full three-section check against a running deployment (pod / vm / local /
// group — the host classifies and routes internally). Mirrors the former in-core CheckLiveCmd
// flags + help so the externalized CLI is behaviour-neutral.
type CheckLiveCmd struct {
	Box      string   `arg:"" help:"Box name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Format   string   `long:"format" default:"text" help:"Output format: text, json, tap"`
	Filter   []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
	Section  string   `long:"section" help:"Only run this section: candy, box, or deploy"`
}

func (c *CheckLiveCmd) Run() error {
	reply, err := hostCheckRun(spec.CheckRunRequest{
		Mode:     "live",
		Name:     c.Box,
		Instance: c.Instance,
		Section:  c.Section,
		Filter:   c.Filter,
	})
	if err != nil {
		return err
	}

	// Nested pod-in-VM delegation: the host ran `charly check live <pod>` IN the guest over SSH
	// and handed back its verbatim stdout/stderr + exit code. Print the host-built banner, forward
	// the guest output unchanged, and map the guest exit to the process-exit convention — the SAME
	// 0-pass / 2-checks-failed / other-infra mapping the former runVm nested-pod tail used.
	if reply.Passthrough != nil {
		if reply.Header != "" {
			fmt.Fprintln(os.Stderr, reply.Header)
		}
		if reply.Passthrough.Stdout != "" {
			fmt.Print(reply.Passthrough.Stdout)
		}
		if reply.Passthrough.Stderr != "" {
			fmt.Fprint(os.Stderr, reply.Passthrough.Stderr)
		}
		switch reply.Passthrough.ExitCode {
		case 0:
			return nil
		case CheckFailExitCode:
			return &CheckFailedError{Failed: 1}
		default:
			return fmt.Errorf("nested-pod check in guest exited %d", reply.Passthrough.ExitCode)
		}
	}

	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps defined for this image.")
		return nil
	}
	if reply.Header != "" {
		fmt.Fprintln(os.Stderr, reply.Header)
	}
	reportSteps(os.Stderr, reply.Steps, c.Format)
	return failErrorFor(reply.Steps)
}
