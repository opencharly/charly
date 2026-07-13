package main

// --- Filename helpers ---

// podQuadletFilename returns the quadlet filename for a pod.
func podQuadletFilename(boxName string) string {
	return PodName(boxName) + ".pod"
}

// podQuadletFilenameInstance returns the quadlet filename for a pod with optional instance.
func podQuadletFilenameInstance(boxName, instance string) string {
	return PodNameInstance(boxName, instance) + ".pod"
}

// sidecarQuadletFilename returns the quadlet filename for a sidecar container.
func sidecarQuadletFilename(boxName, sidecarName string) string {
	return SidecarContainerName(boxName, sidecarName) + ".container"
}

// sidecarQuadletFilenameInstance returns the quadlet filename for a sidecar with optional instance.
func sidecarQuadletFilenameInstance(boxName, instance, sidecarName string) string {
	return SidecarContainerNameInstance(boxName, instance, sidecarName) + ".container"
}
