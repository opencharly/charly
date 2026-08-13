// Package helm is the charly plugin serving the `step:helm-release` install step and
// the `verb:helm` check verb (the helm words, Factory Unit 1) — an importable root
// package + its own go.mod, host-built and served OUT-OF-PROCESS over go-plugin gRPC
// via the charly plugin SDK (github.com/opencharly/sdk), exactly like plugin-kube /
// plugin-wl. It owns NO Kubernetes client library: every operation shells out to the
// venue's `helm` binary over the host's live DeployExecutor reverse channel
// (sdk.ExecutorFromInvoke → RunCapture), so the plugin's go.mod carries no
// k8s.io/client-go dependency.
//
//   - step:helm-release — a plugin-contributed `external:helm-release` install-step KIND
//     (F3): its OPAQUE Payload is the #HelmReleaseStep def, carried through the InstallPlan
//     IR and validated against this plugin's served schema at authoring. The step performs
//     the `helm upgrade --install` invocation IN-VENUE (against the venue's kubeconfig —
//     /etc/rancher/k3s/k3s.yaml on a k3s guest) and returns a teardown ReverseOp
//     (`helm uninstall`) the host records + replays (record-and-replay). StepContract:
//     Scope system, Venue host-native (0), no gate, Emits=false — a DEPLOY-only step
//     (helm installs happen at deploy, never at image build).
//   - verb:helm — the declarative release-status assertion (the `verb:kube` analog): its
//     method + method-exclusive modifiers ride the desugared plugin input, validated
//     against #HelmInput. EXEC-based (like wl/dbus): the host attaches its live
//     DeployExecutor over the E3b reverse channel and this plugin RunCaptures the venue's
//     helm binary; a missing broker is a HARD FAIL.
package helm

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.225.1102"

// NewProvider returns the helm provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the step:helm-release + verb:helm capabilities with the plugin's
// self-contained CUE schema (via sdk.NewMeta → BuildCapabilities). The step carries its
// DECLARED StepContract (Scope system, Venue host-native (0), no gate, Emits=false —
// deploy-only); the verb carries its Primary input field `method` so `helm: release-exists`
// desugars to {method: "release-exists"}.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{
				Class:        "step",
				Word:         "helm-release",
				InputDef:     "#HelmReleaseStep",
				StepContract: &sdk.StepContract{Scope: "system", Venue: 0, Gate: "", Emits: false},
			},
			{
				Class:    "verb",
				Word:     "helm",
				InputDef: "#HelmInput",
				Primary:  "method",
			},
		},
		schemaFS)
}
