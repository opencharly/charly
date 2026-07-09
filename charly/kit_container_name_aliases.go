package main

// kit_container_name_aliases.go — bindings onto the container-naming mechanism
// (containerName/containerNameInstance) moved to sdk/kit in P4.

import "github.com/opencharly/sdk/kit"

var (
	containerName         = kit.ContainerName
	containerNameInstance = kit.ContainerNameInstance
)
