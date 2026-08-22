package spec

// sidecar_naming.go — the pure sidecar/pod container-NAMING helpers (kind-blind string
// composition over ContainerName/ContainerNameInstance), RELOCATED from sdk/kit (#55 value
// extraction). The host-FS SidecarConfigDir helper STAYS in sdk/kit; sdk/kit re-exports these
// pure names so existing kit.PodName / kit.SidecarContainerName… call sites are untouched.

// PodName returns the container name for a pod's primary container.
func PodName(boxName string) string {
	return ContainerName(boxName)
}

// PodNameInstance returns the container name for a pod's primary container, instance-aware.
func PodNameInstance(boxName, instance string) string {
	return ContainerNameInstance(boxName, instance)
}

// SidecarContainerName returns the container name for a named sidecar.
func SidecarContainerName(boxName, sidecarName string) string {
	return ContainerName(boxName) + "-" + sidecarName
}

// SidecarContainerNameInstance returns the container name for a named sidecar, instance-aware.
func SidecarContainerNameInstance(boxName, instance, sidecarName string) string {
	return ContainerNameInstance(boxName, instance) + "-" + sidecarName
}
