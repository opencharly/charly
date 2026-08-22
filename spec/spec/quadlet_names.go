package spec

// quadlet_names.go — the pure quadlet/systemd FILENAME + service-name helpers (kind-blind
// string formatting over ContainerName/PodName/SidecarContainerName), RELOCATED from sdk/kit
// (#55 value extraction). The on-disk quadlet path helpers + host existence probes
// (QuadletDir / SystemdUserDir / QuadletExists[Instance]) ALSO relocated to spec/spec
// (quadlet_paths.go, #55 coneD import-purity); sdk/kit re-exports both so existing
// kit.QuadletFilename / kit.QuadletDir / kit.ServiceName… call sites are untouched.

// ServiceName returns the systemd service name for an image.
func ServiceName(boxName string) string {
	return ContainerName(boxName) + ".service"
}

// ServiceNameInstance returns the systemd service name for an image with optional instance.
func ServiceNameInstance(boxName, instance string) string {
	return ContainerNameInstance(boxName, instance) + ".service"
}

// QuadletFilename returns the quadlet filename for an image.
func QuadletFilename(boxName string) string {
	return ContainerName(boxName) + ".container"
}

// QuadletFilenameInstance returns the quadlet filename for an image with optional instance.
func QuadletFilenameInstance(boxName, instance string) string {
	return ContainerNameInstance(boxName, instance) + ".container"
}

// PodQuadletFilename returns the quadlet filename for a pod.
func PodQuadletFilename(boxName string) string {
	return PodName(boxName) + ".pod"
}

// PodQuadletFilenameInstance returns the quadlet filename for a pod with optional instance.
func PodQuadletFilenameInstance(boxName, instance string) string {
	return PodNameInstance(boxName, instance) + ".pod"
}

// SidecarQuadletFilename returns the quadlet filename for a sidecar container.
func SidecarQuadletFilename(boxName, sidecarName string) string {
	return SidecarContainerName(boxName, sidecarName) + ".container"
}

// SidecarQuadletFilenameInstance returns the quadlet filename for a sidecar with optional instance.
func SidecarQuadletFilenameInstance(boxName, instance, sidecarName string) string {
	return SidecarContainerNameInstance(boxName, instance, sidecarName) + ".container"
}
