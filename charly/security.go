package main

import (
	"strconv"
	"strings"
)

// CollectSecurity merges security configs from all candies in an image,
// then applies image-level overrides. Returns a merged SecurityConfig.
// If any candy sets privileged: true, the result is privileged.
// cap_add, devices, and security_opt are unioned across all candies.
// shm_size takes the largest value from any candy (biggest-wins — more shared
// memory is safer). memory_max, memory_high, memory_swap_max, and cpus take
// the smallest value (smallest-wins — a tighter cap is a smaller blast radius).
// Image-level security (from charly.yml) overrides candy-level settings.
func CollectSecurity(cfg *Config, layers map[string]*Candy, boxName string) SecurityConfig {
	var merged SecurityConfig

	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return merged
	}

	// Resolve the box's own candy tree (leaf-specific — security does NOT
	// inherit from a base box; the shared boxDirectCandies walk). Fall back to
	// the raw direct refs on a resolution error, as before.
	allCandies, err := cfg.boxDirectCandies(layers, boxName)
	if err != nil {
		allCandies = img.Candy
	}

	// Collect from all candies
	for _, candyName := range allCandies {
		ly, ok := layers[candyName]
		if !ok {
			continue
		}
		sec := ly.Security()
		if sec == nil {
			continue
		}
		if sec.Privileged {
			merged.Privileged = true
		}
		if sec.CgroupNS != "" {
			// Explicit declaration wins. Candy order is deterministic,
			// so last-writer semantics are stable; conflicts between
			// candies (both declaring cgroupns, different values) are
			// vanishingly rare and surface immediately at rebuild.
			merged.CgroupNS = sec.CgroupNS
		}
		if sec.IpcMode != "" {
			// Same last-writer semantics as CgroupNS. The harness sandbox
			// declares ipc_mode: host so the nested-podman child can
			// share /dev/shm with the host (chrome / large-shm
			// workloads). When this is set, the quadlet generator
			// MUST drop the ShmSize directive — podman rejects the
			// combination at runtime.
			merged.IpcMode = sec.IpcMode
		}
		merged.CapAdd = appendUnique(merged.CapAdd, sec.CapAdd...)
		merged.Devices = appendUnique(merged.Devices, sec.Devices...)
		merged.SecurityOpt = appendUnique(merged.SecurityOpt, sec.SecurityOpt...)
		merged.GroupAdd = appendUnique(merged.GroupAdd, sec.GroupAdd...)
		merged.Mounts = appendUnique(merged.Mounts, sec.Mounts...)
		if sec.ShmSize != "" {
			merged.ShmSize = maxShmSize(merged.ShmSize, sec.ShmSize)
		}
		if sec.MemoryMax != "" {
			merged.MemoryMax = minCap(merged.MemoryMax, sec.MemoryMax)
		}
		if sec.MemoryHigh != "" {
			merged.MemoryHigh = minCap(merged.MemoryHigh, sec.MemoryHigh)
		}
		if sec.MemorySwapMax != "" {
			merged.MemorySwapMax = minCap(merged.MemorySwapMax, sec.MemorySwapMax)
		}
		if sec.Cpus != "" {
			merged.Cpus = minCpus(merged.Cpus, sec.Cpus)
		}
	}

	// Image-level overrides
	if img.Security != nil {
		merged.Privileged = img.Security.Privileged
		if img.Security.CgroupNS != "" {
			merged.CgroupNS = img.Security.CgroupNS
		}
		if img.Security.IpcMode != "" {
			merged.IpcMode = img.Security.IpcMode
		}
		if len(img.Security.CapAdd) > 0 {
			merged.CapAdd = appendUnique(merged.CapAdd, img.Security.CapAdd...)
		}
		if len(img.Security.Devices) > 0 {
			merged.Devices = appendUnique(merged.Devices, img.Security.Devices...)
		}
		if len(img.Security.SecurityOpt) > 0 {
			merged.SecurityOpt = appendUnique(merged.SecurityOpt, img.Security.SecurityOpt...)
		}
		if img.Security.ShmSize != "" {
			merged.ShmSize = img.Security.ShmSize
		}
		if len(img.Security.GroupAdd) > 0 {
			merged.GroupAdd = appendUnique(merged.GroupAdd, img.Security.GroupAdd...)
		}
		if len(img.Security.Mounts) > 0 {
			merged.Mounts = appendUnique(merged.Mounts, img.Security.Mounts...)
		}
		if img.Security.MemoryMax != "" {
			merged.MemoryMax = img.Security.MemoryMax
		}
		if img.Security.MemoryHigh != "" {
			merged.MemoryHigh = img.Security.MemoryHigh
		}
		if img.Security.MemorySwapMax != "" {
			merged.MemorySwapMax = img.Security.MemorySwapMax
		}
		if img.Security.Cpus != "" {
			merged.Cpus = img.Security.Cpus
		}
	}

	return merged
}

// appendUnique MOVED to sdk/kit (K4 lane B — shared between devices.go/config_image.go's
// continued core use and candy/plugin-deploy-pod's pod_lifecycle_resolve.go move); see
// kit_aliases.go's appendUnique = kit.AppendUnique.

// SecurityArgs/resourceCapArgs MOVED to sdk/deploykit (K4 lane B — shared between
// config_image.go's continued core use and candy/plugin-deploy-pod's pod_lifecycle_resolve.go
// move); see deploykit_pod_aliases.go's SecurityArgs = deploykit.SecurityArgs.

// parseShmBytes parses a size string like "256m", "1g", "1024" into bytes.
func parseShmBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "k")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * multiplier
}

// maxShmSize returns the larger of two shm size strings.
func maxShmSize(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if parseShmBytes(a) >= parseShmBytes(b) {
		return a
	}
	return b
}

// minCap returns the smaller (tighter) of two size-cap strings for memory
// limits — smallest wins because a tighter cap is a smaller blast radius.
// This is the opposite of maxShmSize, which picks the larger shm_size.
func minCap(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if parseShmBytes(a) <= parseShmBytes(b) {
		return a
	}
	return b
}

// minCpus returns the smaller (tighter) of two CPU-quota strings like "2.5".
// Strings that fail to parse are treated as unlimited so the other side wins.
func minCpus(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	av, aerr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	bv, berr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if aerr != nil {
		return b
	}
	if berr != nil {
		return a
	}
	if av <= bv {
		return a
	}
	return b
}
