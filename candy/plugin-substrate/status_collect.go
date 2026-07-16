package substratekind

// status_collect.go — the substrate COLLECTOR OpStatus dispatch. The host's
// status fan-out (charly/status_collector.go collectFlat) reaches the
// cleanly-movable collectors (pod live + local, P14a; vm + k8s, K5) over the
// kind provider's Invoke as sdk.OpStatusCollect, dispatched by word
// (pod/vm/k8s/local/android) — the SAME one-provider-serves-all-5-words shape
// the C2-substrate kind decode uses. android alone is still deferred (its
// collector merges PROJECT + PER-MACHINE deploy config, a slightly deeper
// deploy-cone coupling than vm/k8s needed); the plugin returns no rows for
// that word until its own fold.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// statusCollect dispatches sdk.OpStatusCollect by the reserved substrate word.
// req.GetReserved() is the word (pod/vm/k8s/local/android); req.GetParamsJson()
// is the spec.SubstrateStatusRequest. Returns spec.SubstrateStatusReply as
// ResultJson.
func statusCollect(ctx context.Context, word string, reqJSON []byte) (*statusResult, error) {
	var in spec.SubstrateStatusRequest
	if len(reqJSON) > 0 {
		if err := json.Unmarshal(reqJSON, &in); err != nil {
			return nil, fmt.Errorf("substrate status-collect %q: decode request: %w", word, err)
		}
	}
	switch word {
	case "pod":
		reply, err := collectPodStatus(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("substrate status-collect pod: %w", err)
		}
		return marshalStatusReply(reply)
	case "local":
		reply, err := collectLocalStatus(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("substrate status-collect local: %w", err)
		}
		return marshalStatusReply(reply)
	case "vm":
		reply, err := collectVmStatus(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("substrate status-collect vm: %w", err)
		}
		return marshalStatusReply(reply)
	case "k8s":
		reply, err := collectK8sStatus(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("substrate status-collect k8s: %w", err)
		}
		return marshalStatusReply(reply)
	case "android":
		// K5-gated: the android collector is deploy-cone-coupled
		// (BundleConfig/UnifiedFile + a merge of project + per-machine
		// config) and stays host-side until its own fold. Return no rows;
		// the host's in-proc SubstrateCollector registry still serves it.
		return marshalStatusReply(spec.SubstrateStatusReply{})
	default:
		return nil, fmt.Errorf("substrate status-collect: unsupported word %q", word)
	}
}

// statusResult wraps the marshalled reply so the Invoke switch can return it
// uniformly alongside the kind decode + template resolve legs.
type statusResult struct {
	json []byte
}

func marshalStatusReply(reply spec.SubstrateStatusReply) (*statusResult, error) {
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("substrate status-collect: marshal reply: %w", err)
	}
	return &statusResult{json: out}, nil
}

// OpStatusCollect is the op selector this provider serves for the collector
// capability (mirrors sdk.OpStatusCollect — re-exported here so status_collect
// names the op it dispatches without importing the proto package at every
// call site).
const OpStatusCollect = sdk.OpStatusCollect
