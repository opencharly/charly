package spec

import "strings"

// container_name.go — the deterministic container-naming mechanism (kind-blind string
// formatting), RELOCATED from sdk/kit (#55 value extraction). A deploy key `<base>/<instance>`
// maps to `charly-<key-with-slash-replaced-by-dash>`; sdk/kit re-exports these so existing
// kit.ContainerName / kit.ContainerNameInstance call sites (charly core, sdk/deploykit, the
// candies) are untouched. See /charly-core:deploy.

// ContainerName returns the deterministic container name for an image or a
// `<base>/<instance>` deploy key (the `/` is canonicalized to `-`).
func ContainerName(boxName string) string {
	return "charly-" + strings.ReplaceAll(boxName, "/", "-")
}

// ContainerNameInstance returns the container name with an optional instance suffix.
func ContainerNameInstance(boxName, instance string) string {
	if instance == "" {
		return ContainerName(boxName)
	}
	return "charly-" + strings.ReplaceAll(boxName, "/", "-") + "-" + instance
}
