package vmshared

// Structured <listen> support for LibvirtGraphics.
//
// libvirt's <graphics> element may carry one or more <listen> children.
// The YAML surface accepts three interchangeable shapes for the
// `listen:` field under each graphics entry:
//
//   # (a) scalar address — single <listen type='address' address='.'/>
//   listen: 127.0.0.1
//
//   # (b) single map — explicit type control (socket / address / network)
//   listen:
//     type: socket
//
//   # (c) list — multiple listeners on one <graphics>
//   listen:
//     - type: socket
//     - type: address
//       address: 127.0.0.1
//
// All three unmarshal into LibvirtGraphicsListeners (a list of
// LibvirtGraphicsListen). The libvirt-XML bridge in the VM plugin
// iterates the list and emits one <listen> element per entry.
