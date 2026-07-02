// Egress schema for the rendered libvirt domain XML (RenderDomainXML). Validated
// via CUE's EXPERIMENTAL koala XML encoding: <domain type='kvm'> → domain.$type,
// <name>X</name> → domain.name.$$, <memory unit='KiB'>N</memory> → domain.memory.$$.
// Checks the structural envelope of the rendered domain (non-empty type/name/memory)
// so a broken render or XMLPassthrough snippet fails before VM create. Open (`...`)
// for the large device tree — koala is best-effort (ValidateXMLEgress defers to
// libvirt's DomainDefineXML when koala can't parse), so this is a structural
// envelope, not a full libvirt model. Package-less → joins sharedCueSchema.
#LibvirtDomainXML: {
	domain: {
		"$type": string & !=""
		name: {"$$": string & !="", ...}
		memory: {"$$": string & !="", ...}
		...
	}
	...
}
