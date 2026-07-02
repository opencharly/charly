// This out-of-tree VM plugin's OWN CUE schema, served over the Describe channel.
//
// candy/plugin-vm externalizes the VM subsystem. Its two user-facing capabilities are a
// SCHEMA-LESS verb (libvirt — like cdp/vnc it keeps every modifier on charly's core closed
// #Op, so NO plugin_input / no input def) and a COMMAND (vm — the `charly vm` subcommand tree
// parses its own args out-of-process). Neither carries a #*Input def, so this served schema
// exists ONLY to satisfy the host's non-empty-schema load gate (registerPluginUnitSchema).
// SELF-CONTAINED (package-less, references NO base def) so it compiles standalone AND splices.

// #VmPlugin documents the capabilities the plugin serves. Both keep their authoring contract
// (the libvirt #Op modifiers + the vm command flags) outside this schema.
#VmPlugin: {
	verb:    "libvirt"
	command: "vm"
}
