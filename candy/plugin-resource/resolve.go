package resourcekind

// resolve.go — candy/plugin-resource's OpResolve leg (the resource de-type,
// Cutover G): project an authored spec.Resource into a ResolvedResource the kernel's
// GPU arbiter consumes without importing the concrete kind.

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

func resolveResource(in spec.ResourceResolveInput) (spec.ResourceResolveReply, error) {
	var r spec.Resource
	if err := json.Unmarshal(in.Resource, &r); err != nil {
		return spec.ResourceResolveReply{}, fmt.Errorf("resource resolve: decode: %w", err)
	}
	out := &spec.ResolvedResource{}
	if r.Gpu != nil {
		out.Gpu = &spec.ResolvedGpuSelector{Vendor: r.Gpu.Vendor}
	}
	return spec.ResourceResolveReply{Resolved: out}, nil
}
