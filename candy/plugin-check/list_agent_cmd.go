package check

// list_agent_cmd.go — `charly check list-ai` (P12 Unit iv). Prints the project's
// configured agents (the AI-CLI catalog). PrintAgents relocates here from the
// former core-side agent catalog helper; the OPAQUE kind:agent catalog is read off
// the resolved-project envelope's AgentBodies (the host's uf.PluginKinds["agent"]).
// The kind:agent RESOLVER + the grader are BOTH plugin-side now (agent.go's
// resolveAgentSpec, shared by the harness and the feature-run grader — K1-unblock
// wave arm 2 deleted the last core-side resolver).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

// CheckListAgentCmd implements `charly check list-ai`.
type CheckListAgentCmd struct{}

func (c *CheckListAgentCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	reply, err := resolveCheckProjection(cmdExec, cmdCtx, "", dir)
	if err != nil {
		return err
	}
	PrintAgents(os.Stdout, reply.AgentBodies)
	return nil
}

// PrintAgents writes a human-readable table of configured agents to w. It peeks the
// display fields from each OPAQUE body — the kernel does not type agent bodies (the
// agent de-type, Cutover E).
func PrintAgents(w io.Writer, catalog map[string]json.RawMessage) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No agents configured. Add an 'agent:' map to charly.yml.")
		return
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND\tVERSION_COMMAND\tTIMEOUT\tPROMPT_VIA\tCREDENTIAL")
	for _, name := range names {
		var ai struct {
			Command        []string          `json:"command"`
			PromptVia      string            `json:"prompt_via"`
			VersionCommand []string          `json:"version_command"`
			Timeout        string            `json:"timeout"`
			Credential     []json.RawMessage `json:"credential"`
		}
		_ = json.Unmarshal(catalog[name], &ai)
		timeout := ai.Timeout
		if timeout == "" {
			timeout = DefaultAgentTimeout + " (default)"
		}
		promptVia := ai.PromptVia
		if promptVia == "" {
			promptVia = "argv (default)"
		}
		cmd := strings.Join(ai.Command, " ")
		if len(cmd) > 50 {
			cmd = cmd[:47] + "..."
		}
		ver := strings.Join(ai.VersionCommand, " ")
		if ver == "" {
			ver = "(none)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n",
			name, cmd, ver, timeout, promptVia, len(ai.Credential))
	}
	_ = tw.Flush()
}
