package main

// ports.go — the Config/Candy-coupled (CollectBoxPorts) + deploy-config-coupled
// (SavePortOverride) port helpers that STAY in charly core per the plan (Config is a
// kernel Mechanism, runtime Candy stays core). The pure port-mapping helpers moved to
// sdk/kit in P4; charly binds onto them below.

import (
	"sort"
	"strconv"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

type (
	ParsedPortMapping = kit.ParsedPortMapping
	PortConflict      = kit.PortConflict
)

var (
	ParsePortMapping           = kit.ParsePortMapping
	FormatPortMapping          = kit.FormatPortMapping
	ParseHostPort              = kit.ParseHostPort
	ParseContainerPort         = kit.ParseContainerPort
	IsAutoPort                 = kit.IsAutoPort
	AllocateAutoPorts          = kit.AllocateAutoPorts
	ResolveDeployPorts         = kit.ResolveDeployPorts
	ApplyPortOverrides         = kit.ApplyPortOverrides
	CheckPortAvailability      = kit.CheckPortAvailability
	FindPortOwner              = kit.FindPortOwner
	FormatPortConflicts        = kit.FormatPortConflicts
	ContainerPortsFromMappings = kit.ContainerPortsFromMappings
	SameStringSlice            = kit.SameStringSlice
	stripPortSuffix            = kit.StripPortSuffix
)

func CollectBoxPorts(cfg *Config, layers map[string]spec.CandyReader, boxName string) ([]string, error) {
	names, err := cfg.boxCandyChain(layers, boxName)
	if err != nil {
		return nil, err
	}
	type portEntry struct {
		cp    int
		proto string
	}
	seen := map[int]bool{}
	var entries []portEntry
	for _, candyName := range names {
		layer, ok := layers[candyName]
		if !ok || !layer.HasPorts() {
			continue
		}
		cports, perr := layer.Port()
		if perr != nil {
			continue
		}
		for _, cpStr := range cports {
			clean, proto := kit.StripPortSuffix(cpStr)
			cp, aerr := strconv.Atoi(clean)
			if aerr != nil || cp <= 0 || cp > 65535 || seen[cp] {
				continue
			}
			seen[cp] = true
			entries = append(entries, portEntry{cp: cp, proto: proto})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].cp < entries[j].cp })
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		s := strconv.Itoa(e.cp)
		if e.proto != "" {
			s += "/" + e.proto
		}
		out = append(out, s)
	}
	return out, nil
}

func SavePortOverride(box, instance string, ports []string) error {
	dc, err := deploykit.LoadDeployConfigForWrite("SavePortOverride")
	if err != nil {
		return err
	}

	key := deploykit.DeployKey(box, instance)
	overlay := dc.Bundle[key]
	overlay.Port = ports
	dc.Bundle[key] = overlay

	return saveBundleConfigNodeForm(dc)
}
