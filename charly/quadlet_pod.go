package main

import "github.com/opencharly/sdk/kit"

// --- Filename helpers ---

// podQuadletFilename returns the quadlet filename for a pod.
func podQuadletFilename(boxName string) string {
	return kit.PodName(boxName) + ".pod"
}

// podQuadletFilenameInstance returns the quadlet filename for a pod with optional instance.
func podQuadletFilenameInstance(boxName, instance string) string {
	return kit.PodNameInstance(boxName, instance) + ".pod"
}

// sidecarQuadletFilename returns the quadlet filename for a sidecar container.
func sidecarQuadletFilename(boxName, sidecarName string) string {
	return kit.SidecarContainerName(boxName, sidecarName) + ".container"
}

// sidecarQuadletFilenameInstance returns the quadlet filename for a sidecar with optional instance.
func sidecarQuadletFilenameInstance(boxName, instance, sidecarName string) string {
	return kit.SidecarContainerNameInstance(boxName, instance, sidecarName) + ".container"
}
