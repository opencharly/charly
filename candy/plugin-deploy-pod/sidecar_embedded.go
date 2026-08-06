package deploypod

import (
	_ "embed"
	"encoding/json"
)

// sidecar_embedded.go — the sidecar-template library RELOCATED from charly/sidecar.go's
// embeddedSidecarBodies + the deleted "pod-config-list-sidecars" HostBuild seam (K-wave 2 cone
// R3): the binary-embedded default sidecar templates (the tailscale default) now live in THIS
// plugin's own go:embed (a separate Go module cannot go:embed charly-core's charly.yml), served
// to resolvePodSidecars (sidecar_resolve.go) and the `charly config --list-sidecars` leaf
// (config_setup.go's OpConfigSetup ListSidecars branch) — the SAME name → opaque-body map shape
// the former seam's BodiesJSON carried.

//go:embed sidecar_templates.json
var embeddedSidecarTemplates []byte

// embeddedSidecarLibrary returns the binary-embedded sidecar-template library as a
// name → opaque-body map (spec.Sidecar JSON, the shape kind:sidecar's OpResolve merges under a
// deploy's own overrides). The tailscale template was moved out of charly/charly.yml into this
// embed verbatim (byte-identical bodies — verified against the loader's canonical output), so
// removing the sidecar section from core's embedded defaults loses nothing.
func embeddedSidecarLibrary() (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(embeddedSidecarTemplates, &m); err != nil {
		return nil, err
	}
	return m, nil
}
