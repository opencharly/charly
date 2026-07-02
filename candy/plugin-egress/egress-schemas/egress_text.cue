// Egress schema for rendered NON-DATA text artifacts — the Containerfile and the
// systemd/supervisord service units charly produces via Go text/template. charly
// does NOT set the template `missingkey=error` option, so a nil field renders the
// literal marker "<no value>" into the output. #RenderedText rejects that marker,
// turning a silent template render failure into a hard build/deploy error instead
// of a broken artifact. Package-less → joins sharedCueSchema, resolved via
// egressDef's fallback. (Hand-built text — quadlet/shell/ssh/qemu — uses no
// template, so it has no "<no value>" failure mode and is not gated here.)
#RenderedText: string & !~"<no value>"
