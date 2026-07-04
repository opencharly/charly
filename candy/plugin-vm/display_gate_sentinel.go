package vm

// noVmDisplayDeviceErr is the substring the VM-target display resolvers (SpiceEndpoint/VncEndpoint)
// embed in their "no <graphics> device" errors, and the host's preresolveSpiceEndpoint keys off to
// return a SKIP (not a FAIL) for a display-less VM (the SPICE/VNC-less cachyos-gpu operator). It is
// the wire contract between this out-of-process vm plugin and charly's core, which keeps its own
// copy in checkrun_charly_verbs.go (separate modules cannot share a package-main const).
const noVmDisplayDeviceErr = "graphics device declared in vm.yml"
