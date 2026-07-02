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

	img, ok := cfg.Box[boxName]
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

// appendUnique appends items to a slice, skipping duplicates.
func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range items {
		if !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}

// SecurityArgs returns the container run arguments for the given security config.
//
// Note on the ShmSize+IpcMode interaction: podman rejects `--shm-size`
// when the IPC namespace is shared with the host (`--ipc=host`)
// because the host's /dev/shm is shared in-kernel and sized by the
// host kernel; an explicit shm-size on the container makes no sense
// in that case and yields a runtime error like "cannot set shmsize
// when running in the {host} IPC Namespace". Same logic applies to
// the quadlet generator's ShmSize= directive elsewhere.
func SecurityArgs(sec SecurityConfig) []string {
	emitShmSize := sec.ShmSize != "" && !ipcModeBlocksShmSize(sec.IpcMode)
	if sec.Privileged {
		args := []string{"--privileged"}
		// Pass security_opt even when privileged — nested containers need
		// explicit label=disable and seccomp=unconfined since --privileged
		// alone doesn't propagate through container nesting levels.
		for _, opt := range sec.SecurityOpt {
			args = append(args, "--security-opt", opt)
		}
		if sec.CgroupNS != "" {
			args = append(args, "--cgroupns", sec.CgroupNS)
		}
		if sec.IpcMode != "" {
			args = append(args, "--ipc", sec.IpcMode)
		}
		if emitShmSize {
			args = append(args, "--shm-size", sec.ShmSize)
		}
		args = append(args, resourceCapArgs(sec)...)
		return args
	}
	var args []string
	for _, cap := range sec.CapAdd {
		args = append(args, "--cap-add", cap)
	}
	for _, dev := range sec.Devices {
		args = append(args, "--device", dev)
	}
	for _, opt := range sec.SecurityOpt {
		args = append(args, "--security-opt", opt)
	}
	for _, group := range sec.GroupAdd {
		args = append(args, "--group-add", group)
	}
	if sec.CgroupNS != "" {
		args = append(args, "--cgroupns", sec.CgroupNS)
	}
	if sec.IpcMode != "" {
		args = append(args, "--ipc", sec.IpcMode)
	}
	if emitShmSize {
		args = append(args, "--shm-size", sec.ShmSize)
	}
	args = append(args, resourceCapArgs(sec)...)
	return args
}

// ipcModeBlocksShmSize reports whether the IPC namespace mode forces
// the shm-size directive to be omitted (podman rejects shm-size when
// the IPC namespace is shared with the host kernel).
//
// Currently only "host" qualifies. "shareable" / "private" / "" all
// allow per-container shm-size sizing.
func ipcModeBlocksShmSize(ipcMode string) bool {
	return ipcMode == "host"
}

// resourceCapArgs returns the podman run flags for memory and CPU caps.
// Emitted identically in both the privileged and non-privileged branches
// of SecurityArgs because privileged containers still need resource limits.
func resourceCapArgs(sec SecurityConfig) []string {
	var args []string
	if sec.MemoryMax != "" {
		args = append(args, "--memory", sec.MemoryMax)
	}
	if sec.MemoryHigh != "" {
		args = append(args, "--memory-reservation", sec.MemoryHigh)
	}
	if sec.MemorySwapMax != "" {
		args = append(args, "--memory-swap", sec.MemorySwapMax)
	}
	if sec.Cpus != "" {
		args = append(args, "--cpus", sec.Cpus)
	}
	return args
}

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
