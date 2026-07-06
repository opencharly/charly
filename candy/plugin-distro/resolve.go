package distrokind

// resolve.go — candy/plugin-distro's OpResolve leg (the distro de-type, Cutover M):
// project an authored spec.Distro into a ResolvedDistro the kernel's build engine
// consumes without importing the concrete kind. Field-copy: the host keeps
// RenderTemplate + the cache-mount vocab; the plugin owns the distro knowledge.

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

func resolveDistro(in spec.DistroResolveInput) (spec.DistroResolveReply, error) {
	var d spec.Distro
	if err := json.Unmarshal(in.Distro, &d); err != nil {
		return spec.DistroResolveReply{}, fmt.Errorf("distro resolve: decode: %w", err)
	}
	return spec.DistroResolveReply{Resolved: &spec.ResolvedDistro{
		Inherits:        d.Inherits,
		InheritPackages: d.InheritPackages,
		Version:         d.Version,
		Bootstrap:       d.Bootstrap,
		Workarounds:     d.Workarounds,
		Format:          d.Format,
		BaseUser:        d.BaseUser,
		Pacstrap:        d.Pacstrap,
		Debootstrap:     d.Debootstrap,
		AlpineBootstrap: d.AlpineBootstrap,
		Bootloader:      d.Bootloader,
		Dnf:             d.Dnf,
		Raw:             in.Distro,
	}}, nil
}
