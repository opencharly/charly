// Egress schemas for charly's OWN simple cloud-init seed files. Package-less, so
// these join the concatenated sharedCueSchema (cue_schema.go) and resolve via
// egressDef's cueKindDef fallback — unlike the vendored #CloudConfig (user-data),
// which carries a package+imports and compiles as its own instance.
//
// These validate exactly what charly's RenderCloudInit emits (closed to those
// keys): if the renderer grows a field, the schema grows with it.

// #CloudInitMeta — the NoCloud meta-data document (instance-id + hostname).
#CloudInitMeta: close({
	"instance-id"?:    string & !=""
	"local-hostname"?: string & !=""
})

// #NetworkConfigV2 — cloud-init network-config. charly emits only version +
// a mapping of named ethernet devices; device bodies are left open.
#NetworkConfigV2: close({
	version: int
	ethernets?: {...}
})
