package preempt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// command.go — the externalized `charly preempt` command (status / restore). The plugin OWNS the CLI
// grammar + the lease-table formatting; it reaches the arbiter — its OWN peer capability verb:arbiter
// (compiled-in) — DIRECTLY via InvokeProvider over the in-proc reverse channel. No hidden `__preempt-*`
// forward, no in-core proxy hop: command:preempt → InvokeProvider → verb:arbiter → (HostArbiter → core
// host seams). preempt is the first compiled-in COMMAND that reaches a peer VERB plugin over
// InvokeProvider (the HostBuild commands reach a terminal host builder; this reaches another plugin).
// COMPILED-IN because Invoke(OpRun) needs the reverse channel; out-of-process CliMain errors.

const preemptUsage = `usage: charly preempt <status | restore [claimant]>`

// runPreemptCLI dispatches the preempt subcommand, reaching verb:arbiter via InvokeProvider and owning
// the output (the lease table + the restore messages).
func runPreemptCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", preemptUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Println(preemptUsage)
		return nil
	case "status":
		reply, err := arbiterAction(ctx, exec, spec.ArbiterInvokeInput{Action: spec.ArbiterActionStatus})
		if err != nil {
			return err
		}
		ledger := reply.Ledger
		if ledger == nil {
			ledger = &spec.PreemptLedger{}
		}
		return renderLeaseTable(ledger, reply.Stranded, os.Stdout)
	case "restore":
		claimant := ""
		if len(rest) == 1 {
			claimant = rest[0]
		} else if len(rest) > 1 {
			return fmt.Errorf("usage: charly preempt restore [claimant]")
		}
		if claimant == "" {
			if _, err := arbiterAction(ctx, exec, spec.ArbiterInvokeInput{Action: spec.ArbiterActionReconcile}); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "preempt: reconciled stranded leases — holders for any departed claimant restored.")
		} else {
			if _, err := arbiterAction(ctx, exec, spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: true}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "preempt: released lease for %q and restored its holders.\n", claimant)
		}
		return nil
	default:
		return fmt.Errorf("unknown preempt subcommand %q\n%s", sub, preemptUsage)
	}
}

// arbiterAction invokes the peer verb:arbiter over the reverse channel with an action-tagged input.
// exec is nil on the out-of-process cliMain path (no reverse channel) → a clear error.
func arbiterAction(ctx context.Context, exec *sdk.Executor, in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	if exec == nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("charly preempt requires compiled-in placement (the arbiter reverse channel is unavailable out-of-process)")
	}
	params, err := json.Marshal(in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, err
	}
	out, err := exec.InvokeProvider(ctx, "verb", "arbiter", sdk.OpRun, params, nil)
	if err != nil {
		return spec.ArbiterInvokeReply{}, err
	}
	var reply spec.ArbiterInvokeReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return spec.ArbiterInvokeReply{}, uerr
		}
	}
	if reply.Error != "" {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}

// renderLeaseTable prints the arbiter's lease ledger + flags stranded leases (moved from charly core —
// it reads only spec types, so it is pure plugin-side formatting).
func renderLeaseTable(ledger *spec.PreemptLedger, stranded []string, out io.Writer) error {
	if ledger == nil || len(ledger.Leases) == 0 {
		fmt.Fprintln(out, "No active preemption leases.")
		return nil
	}
	strandedSet := map[string]bool{}
	for _, s := range stranded {
		strandedSet[s] = true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLAIMANT\tTOKENS\tTRANSIENT\tPREEMPTED HOLDERS\tCREATED\tSTATE")
	for _, lz := range ledger.Leases {
		holders := make([]string, 0, len(lz.Preempted))
		for _, ph := range lz.Preempted {
			holders = append(holders, ph.Addr.Name)
		}
		hs := strings.Join(holders, ",")
		if hs == "" {
			hs = "-"
		}
		state := "active"
		if strandedSet[lz.Claimant] {
			state = "STRANDED — run `charly preempt restore`"
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\n",
			lz.Claimant, strings.Join(lz.Tokens, ","), lz.Transient, hs, lz.Created, state)
	}
	return tw.Flush()
}
