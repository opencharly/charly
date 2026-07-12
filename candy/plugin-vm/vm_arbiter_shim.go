package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// vm_arbiter_shim.go — the moved VM CLI's preempt/resource-arbitration reach. The arbiter LOGIC lives
// in candy/plugin-preempt (verb:arbiter); `charly vm create/stop/destroy/gpu` reach it over the reverse
// channel (InvokeProvider — the plugin-preempt arbiterAction pattern) instead of core's in-package
// arbiterProxy. lookupVMClaimant is a config read → the config-resolve seam.

const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// arbiterInvoke reaches verb:arbiter with an action-tagged input (never-fail carrier: error rides reply.Error).
func arbiterInvoke(in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	if cmdExec == nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("vm preempt: no host reverse channel (command not compiled-in?)")
	}
	params, err := json.Marshal(in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, err
	}
	out, err := cmdExec.InvokeProvider(cmdCtx, "verb", "arbiter", sdk.OpRun, params, nil)
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
		return spec.ArbiterInvokeReply{}, errors.New(reply.Error)
	}
	return reply, nil
}

// lookupVMClaimant resolves whether a deploy/check node claims this VM via requires_exclusive — a
// config read routed through the config-resolve seam (the loader is a core Mechanism).
func lookupVMClaimant(box string) (string, BundleNode, bool) {
	reply, err := hostConfigResolve(box)
	if err != nil || reply.Claimant == "" || reply.ClaimantNode == nil {
		return "", BundleNode{}, false
	}
	return reply.Claimant, *reply.ClaimantNode, true
}

// holderAddrFor computes the preempt holder address for a vm claimant (the ssh/vm venue case).
func holderAddrFor(name string, node BundleNode) spec.HolderAddr {
	base := strings.TrimPrefix(name, "vm:")
	vm := node.From
	if vm == "" {
		vm = base
	}
	return spec.HolderAddr{Name: name, Target: node.Target, Base: base, Vm: vm}
}

// Lease is the acquire result (minimal — mirrors core's shape for the callers that ignore it).
type Lease struct {
	claimant string
	active   bool
}

// acquireExclusiveForClaimant acquires the exclusive lease for a claimant that needs it (no-op when the
// node declares no requires_exclusive — the non-GPU path every vm bed hits — or an outer lease is held).
func acquireExclusiveForClaimant(claimant string, node BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredExclusive()) == 0 {
		return &Lease{}, nil
	}
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil
	}
	r, err := arbiterInvoke(spec.ArbiterInvokeInput{
		Action:    spec.ArbiterActionAcquireExclusive,
		Claimant:  claimant,
		Tokens:    dedupeNonEmpty(node.RequiredExclusive()),
		ClaimAddr: holderAddrFor(claimant, node),
		Transient: transient,
	})
	if err != nil {
		return nil, err
	}
	if r.Active {
		_ = os.Setenv(envPreemptLeaseHeld, claimant)
	}
	return &Lease{claimant: claimant, active: r.Active}, nil
}

// releaseResourceClaim releases this VM's exclusive claim (restoring any preempted holder host-side).
func releaseResourceClaim(claimant string) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return
	}
	if _, err := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: true}); err != nil {
		fmt.Fprintf(os.Stderr, "preempt: %v\n", err)
	}
}

// resourceArbiter is the plugin-side arbiter handle — its methods dispatch verb:arbiter actions.
type resourceArbiter struct{}

func newResourceArbiter() *resourceArbiter { return &resourceArbiter{} }

func (*resourceArbiter) ReleaseClaimant(claimant string, success bool) error {
	_, err := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: success})
	return err
}

func (*resourceArbiter) clearPoison(token string) {
	_, _ = arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionClearPoison, Token: token})
}

func (*resourceArbiter) resourcePoisoned(token string) bool {
	r, _ := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionResourcePoisoned, Token: token})
	return r.Bool
}
