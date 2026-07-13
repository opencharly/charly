// Package oci is the importable form of charly's OCI ENGINE plugin: it OWNS the
// go-containerregistry layer-MERGE engine (merge.go: planMerge → executeMerge →
// mergeLayers, plus the podman/skopeo daemon load/save) and the remote-image
// USER-PROBE (inspect_user.go: /etc/passwd adopt-user lookup) that charly's build
// engine needs. Both run HOST-SIDE and exec podman/skopeo themselves — the
// verb:libvirt precedent — so the go-containerregistry stack lives HERE, not in
// charly core.
//
// ONE provider, verb:oci, dual-placement-capable (NewProvider/NewMeta compile INTO
// charly when listed in compiled_plugins — the DEFAULT, mirroring the host-coupled
// verb:libvirt / verb:tunnel / verb:enc profile, since the merge + adopt-user probes
// sit on the CORE BUILD PATH and must resolve project-lessly and reliably; or
// cmd/serve serves them OUT-OF-PROCESS on a custom build that omits it). The verb is
// NOT authored as a `oci:` check step; it is a pure INTERNAL RPC verb reached by the
// host's merge/inspect-user consumers, keyed by an OciOp env discriminator (mirroring
// the vm plugin's VmOp) — so it declares NO InputDef and ships NO schema (the load
// gate waives an input-less plugin). A standalone Go module (its own go.mod) carrying
// the go-containerregistry stack, so charly/go.mod links go-containerregistry nowhere.
package oci

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is this plugin candy's CalVer identity (matches charly.yml version:).
const calver = "2026.194.1200"

// NewProvider returns the oci provider (verb:oci — the merge + inspect-user internal ops).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:oci. InputDef is "" (no authored `oci:` check step — the
// verb is a pure INTERNAL RPC keyed by the OciOp env discriminator), so the plugin
// ships NO CUE schema — BuildCapabilities waives the schema for an input-less plugin
// (nil schemaFS).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "oci"}},
		nil)
}

type provider struct{ pb.UnimplementedProviderServer }

// ociEnv is the plugin-internal env discriminator (mirrors the vm plugin's VmOp):
// OciOp selects the leg. The op's request struct (spec.MergeRequest /
// spec.ImageUserInput) rides Params; this env carries only the selector.
type ociEnv struct {
	// OciOp selects the internal op: "merge" | "inspect-user".
	OciOp string `json:"oci_op"`
}

// Invoke dispatches an internal oci op keyed by the OciOp env discriminator. Both
// the host's merge consumer (charly box merge / the build-drive InvokeProvider) and
// the build engine's inspect-user consumer reach the SAME OpRun surface — the leg is
// chosen by env.OciOp, NOT an sdk.Op selector.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("oci: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var env ociEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("oci: decode env: %w", err)
		}
	}
	switch env.OciOp {
	case "merge":
		return mergeLeg(req.GetParamsJson())
	case "inspect-user":
		return inspectUserLeg(req.GetParamsJson())
	default:
		return nil, fmt.Errorf("oci: unknown oci_op %q (want merge|inspect-user)", env.OciOp)
	}
}
