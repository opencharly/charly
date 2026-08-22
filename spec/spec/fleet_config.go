package spec

import "encoding/json"

// fleet_config.go — SPIKE: FleetConfig relocated from sdk/deploykit/deploy_state.go
// (#55 value-type relocation spike, cluster 2). Every field already resolved to a
// spec.* type (Provides=*spec.ProvidesConfig, Fleet=map[string]spec.FleetNode(=Deploy),
// Sidecar=map[string]json.RawMessage) so the type carries zero deploykit-only content.
// deploykit.FleetConfig becomes a type alias onto this type. Only the two PURE
// methods (Lookup/LookupKey, plain map access) moved with the type — the three
// methods that reach sdk/kit (DeployedContainerNames/OccupiedHostPorts/
// GlobalEnvForImage) stay in deploykit as free functions taking *spec.FleetConfig
// (spec can never import sdk/kit — the method-set cycle the spike flagged).

// FleetConfig represents per-machine deployment overrides (~/.config/charly/charly.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
//
// Schema v4: the top-level map key is `deployment:` (singular, flat). The
// legacy `images:` / `deployments.images.*` nesting is gone — all target
// kinds (host / vm / pod / kubernetes) live under the single `deployment:` map.
type FleetConfig struct {
	Provides *ProvidesConfig      `yaml:"provides,omitempty" json:"provides,omitempty"`
	Fleet    map[string]FleetNode `yaml:"deploy" json:"deploy"`
	// Sidecar carries the project's sidecar-template library as OPAQUE bodies
	// (the raw PluginKinds["sidecar"] map). candy/plugin-sidecar's OpResolve merges
	// these UNDER each deploy node's own overrides; the kernel reads no fields
	// (the sidecar de-type, Cutover D).
	Sidecar map[string]json.RawMessage `yaml:"sidecar,omitempty" json:"sidecar,omitempty"`
}

// Lookup returns the FleetNode for (deployName, instance), or
// (zero, false) when the entry is absent. Safe to call on a nil
// *FleetConfig — lets callers chain
// `loadDeployConfigForRead(...).Lookup(deployName, instance)` without a
// separate nil check. deployName is the charly.yml key base the caller is
// operating on (typically c.Image), NOT the baked image short-name — for a
// kind:check bed or Pattern-B deploy the two differ. Pass the deploy key, never
// a value derived from an image label (see MergeDeployOntoMetadata).
func (dc *FleetConfig) Lookup(deployName, instance string) (FleetNode, bool) {
	if dc == nil {
		return FleetNode{}, false
	}
	entry, ok := dc.Fleet[DeployKey(deployName, instance)]
	return entry, ok
}

// LookupKey looks up a deploy entry by its full charly.yml key (e.g.
// "foo", "foo/instance", "vm:name"). Safe on nil receiver.
func (dc *FleetConfig) LookupKey(key string) (FleetNode, bool) {
	if dc == nil {
		return FleetNode{}, false
	}
	entry, ok := dc.Fleet[key]
	return entry, ok
}
