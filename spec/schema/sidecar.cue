// CUE schema for the `sidecar` kind. #Sidecar validates ONE sidecar-template
// entity (a value of the `sidecar:` map, e.g. the embedded default library, or
// a per-deploy override). Mirrors Go SidecarDef. CLOSED (an unknown key is a
// typo). Every field optional (struct tags are omitempty; an override supplies a
// subset).

#Sidecar: {
	description?: string & !=""
	image?:       string & !=""
	// env / parameter are map[string]string — values MUST be strings (quote
	// YAML bools/numbers). parameter "" is the "deploy must supply" sentinel.
	env?:       #StrMap
	parameter?: #StrMap
	secret?: [...#SidecarSecret]
	volume?: [...#SidecarVolume]
	security?: #Security @go(Security,optional=nillable)
}

#SidecarSecret: {
	name:         string & !=""
	env:          string & !=""
	env_from?:    string @go(EnvFrom) // Go text/template, rendered later; CUE checks string only
	description?: string & !=""
}

#SidecarVolume: {
	name: string & !=""
	path: string & =~"^/"
}

// #Security + #Size now live in _common.cue (shared by box/candy/deploy/sidecar).

// ---------------------------------------------------------------------------
// Resolve-to-envelope wire types (Cutover D; SDD conversion, per the standing
// operator directive: a hand-written wire struct not yet CUE-sourced is
// conversion-in-progress, never a sanctioned exception). candy/plugin-sidecar
// owns ALL sidecar business logic (env-flag routing, template merge,
// volume/secret-name + env_from resolution); the host stays OPAQUE to sidecar
// bodies (it never reads their fields). Written out explicitly (not embedding
// #Sidecar, whose field set + required-ness differs from the resolved
// envelope) so every field's state is independently auditable against the
// former hand type. Plain structs — gengotypes generates them faithfully, no
// disjunction needed.

// #SidecarResolveInput is the input to candy/plugin-sidecar's OpResolve leg
// (the host-side sidecar de-type): the three sidecar-def layers to merge
// (embedded template base, project-root templates, per-deploy overrides —
// each a name→Sidecar map the host keeps OPAQUE) plus the CLI -e flags to
// route and the box/instance for name scoping.
#SidecarResolveInput: {
	embedded_templates?: {[string]: bytes} @go(EmbeddedTemplates,type=map[string]RawBody)
	project_templates?: {[string]: bytes} @go(ProjectTemplates,type=map[string]RawBody)
	deploy_overrides?: {[string]: bytes} @go(DeployOverrides,type=map[string]RawBody)
	cli_env?: [...string] @go(CliEnv)
	box?:      string @go(Box)
	instance?: string @go(Instance)
}

// #SidecarResolveReply is candy/plugin-sidecar's OpResolve reply: the
// resolved, generation-ready sidecars the host feeds to quadlet gen, the CLI
// env flags NOT routed to any sidecar (app-only), and the per-deploy sidecar
// overrides with any routed env folded in.
#SidecarResolveReply: {
	sidecars?: [...#ResolvedSidecar] @go(Sidecars)
	app_env?: [...string] @go(AppEnv)
	persist_overrides?: {[string]: bytes} @go(PersistOverrides,type=map[string]RawBody)
}

// #ResolvedSidecar is a fully-resolved co-deployed container the host
// consumes for quadlet generation — the resolve-to-envelope form of a
// sidecar (NO Sidecar / SidecarDef fields survive; the plugin already merged
// + resolved everything).
#ResolvedSidecar: {
	name!: string @go(Name)
	image?: string @go(Image)
	env?: {[string]: string} @go(Env)
	secret?: [...#ResolvedSidecarSecret] @go(Secret)
	volume?: [...#ResolvedSidecarVolume] @go(Volume)
	security?: #Security @go(Security,optional=nillable)
}

// #ResolvedSidecarSecret is a resolved sidecar secret (the sidecar-scoped
// subset of the host's CollectedSecret): the podman secret name + the
// container/host env-var names + the original manifest secret name.
#ResolvedSidecarSecret: {
	name!: string @go(Name)
	env?:         string @go(Env)
	host_env?:    string @go(HostEnv)
	secret_name?: string @go(SecretName)
}

// #ResolvedSidecarVolume is a resolved sidecar volume: the charly-scoped
// volume name + its container mount path.
#ResolvedSidecarVolume: {
	volume_name!:    string @go(VolumeName)
	container_path!: string @go(ContainerPath)
}
