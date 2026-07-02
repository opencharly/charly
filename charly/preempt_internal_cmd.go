package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/opencharly/charly/charly/spec"
)

// preempt_internal_cmd.go implements the two HIDDEN core commands that expose the resource
// arbiter to the externalized `charly preempt …` COMMAND plugin (candy/plugin-preempt). Since
// cutover C9 the arbiter LOGIC lives IN that plugin (verb:arbiter); these hidden verbs reach it
// through the in-core PROXY (preempt.go newResourceArbiter), so the operator-facing
// `charly preempt status` / `charly preempt restore` CLI is byte-identical while the
// implementation is out of the core binary. The SAME `charly __cli-model` / `__plugin-providers`
// internal-command pattern.

// PreemptStatusInternalCmd: `charly __preempt-status` (hidden machinery). Prints the active
// resource-arbitration leases exactly as the former `charly preempt status` did.
type PreemptStatusInternalCmd struct{}

func (PreemptStatusInternalCmd) Run() error {
	ledger, stranded, err := newResourceArbiter().Status()
	if err != nil {
		return err
	}
	return renderLeaseTable(ledger, stranded, os.Stdout)
}

// renderLeaseTable formats a lease ledger + its stranded-claimant set as the active-lease table.
// Split from the arbiter fetch so a unit test drives it with a hand-built ledger (no live
// plugin needed) — the fetch (the proxy Status() dispatch) is exercised by the R10 bed.
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

// PreemptRestoreInternalCmd: `charly __preempt-restore [claimant]` (hidden machinery). Recovers
// preempted holders exactly as the former `charly preempt restore` did. With no argument it
// reconciles every stranded lease (claimant gone); with a claimant argument it force-releases
// that lease and restarts its holders regardless of the `restore:` policy.
type PreemptRestoreInternalCmd struct {
	Claimant string `arg:"" optional:"" help:"Claimant whose lease to force-restore; omit to reconcile ALL stranded leases"`
}

func (c *PreemptRestoreInternalCmd) Run() error {
	a := newResourceArbiter()
	if c.Claimant == "" {
		if err := a.reconcileStranded(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "preempt: reconciled stranded leases — holders for any departed claimant restored.")
		return nil
	}
	if err := a.ReleaseClaimant(c.Claimant, true); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "preempt: released lease for %q and restored its holders.\n", c.Claimant)
	return nil
}
