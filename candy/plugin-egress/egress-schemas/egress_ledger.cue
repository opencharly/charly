// Egress schemas for the install-ledger records charly writes under
// ~/.config/opencharly/installed/ (deploys/<id>.json + layers/<candy>.json). The
// ledger drives teardown (ReverseOps), so a record missing its identity/time
// fields would silently break `charly bundle del` / `charly update`. These
// validate the record ENVELOPE (required fields non-empty) before the JSON hits
// disk — host (writeJSONAtomic) and guest (executor heredoc) writers alike.
// steps/reverse_ops are charly-internal lists, left open. Package-less → these
// join sharedCueSchema and resolve via egressDef's fallback.

#DeployRecord: {
	// deploy_id (the filename + refcount key), target (the deploy venue), and
	// deployed_at are always set. image is OPTIONAL: a candy-only host/vm deploy
	// (add_candy onto an existing target, no container image) legitimately leaves
	// it empty — the deploy_id (not the image) is the required ledger key.
	deploy_id:       string & !=""
	target:          string & !=""
	deployed_at:     string & !=""
	image?:          string
	schema_version?: string
	tag?:            string
	candy?: [...string]
	add_candy?: [...string]
	...
}

#CandyRecord: {
	candy:           string & !=""
	deployed_at:     string & !=""
	schema_version?: string
	version?:        string
	builder_image?:  string
	deployed_by?: [...string]
	steps?: [...{...}]
	reverse_ops?: [...{...}]
	...
}
