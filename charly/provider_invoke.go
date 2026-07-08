package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// provider_invoke.go — the ONE host→plugin call codec (R3). Before this file, ~20
// host-side shims each hand-rolled the same resolve→marshal→Invoke→decode skeleton
// (egress, gpu, the substrate/resource/distro/sidecar/init/agent kind resolvers, and
// the k8s/tunnel/enc/credential/arbiter verb adapters). They now share invokeTyped
// (the codec) + hostInvoke / hostInvokeOr (resolve + codec). A site with a SPECIAL
// resolve/connect (a connectPluginByWord lazy fallback, a ctx-threaded reverse
// channel, a label-prefixed miss message) keeps that per-site and routes only the
// codec through invokeTyped; the plain-resolve, typed-reply sites use hostInvoke /
// hostInvokeOr wholesale.

// invokeTyped is THE codec for a host-side call onto an ALREADY-RESOLVED provider:
// marshal `in` into Operation.Params, Invoke(word/op) on ctx, and decode the JSON
// reply into Out. ctx threads any caller context (a cancellable ctx for the
// credential await-unlock, an in-proc reverse-channel executor for the arbiter). A
// nil/empty reply body yields the zero Out — the guard every former hand skeleton
// carried.
func invokeTyped[In, Out any](ctx context.Context, prov Provider, word, op string, in In) (Out, error) {
	var out Out
	params, err := marshalJSON(in)
	if err != nil {
		return out, err
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: op, Params: params})
	if err != nil {
		return out, err
	}
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// hostInvoke resolves (class, word) in the provider registry and calls invokeTyped
// with a background context. A resolve miss returns an error naming the missing
// plugin candy. A site with a lazy connect (connectPluginByWord) or a
// context/env-threaded call resolves/connects per-site and calls invokeTyped directly.
func hostInvoke[In, Out any](class ProviderClass, word, op string, in In) (Out, error) {
	var out Out
	prov, ok := providerRegistry.resolve(class, word)
	if !ok {
		return out, fmt.Errorf("%s plugin (%s:%s) not registered — charly built without the plugin candy that serves it", word, class, word)
	}
	return invokeTyped[In, Out](context.Background(), prov, word, op, in)
}

// hostInvokeOr is the best-effort twin of hostInvoke: a resolve miss / marshal /
// invoke / decode failure logs ONE stderr warning prefixed with warnLabel and returns
// the zero Out — for hot probe paths that must never fail (the gpu-detection
// semantics: degrade to "no devices", never crash the deploy).
func hostInvokeOr[In, Out any](class ProviderClass, word, op string, in In, warnLabel string) Out {
	var zero Out
	prov, ok := providerRegistry.resolve(class, word)
	if !ok {
		fmt.Fprintf(os.Stderr, "warning: %s: plugin (%s:%s) not registered\n", warnLabel, class, word)
		return zero
	}
	out, err := invokeTyped[In, Out](context.Background(), prov, word, op, in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: %v\n", warnLabel, err)
		return zero
	}
	return out
}
