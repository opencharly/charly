package spec

import "strings"

// StatusFromState normalises an engine/container runtime state vocabulary to
// charly's DeploymentStatus.Status vocabulary. Shared by the substrate plugins'
// status collectors (the pod live row builder in candy/plugin-substrate) AND the
// host-side vm collector (charly/status_collect_vm.go, deploy-cone-coupled →
// stays core until K5) — single-sourced here (R3) so the live/enrich split
// (P14a) and the K5 vm-collector move both read ONE mapper. Pure over its input.
func StatusFromState(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "running"
	case "exited", "stopped", "created":
		return "stopped"
	case "dead":
		return "dead"
	case "removing":
		return "removing"
	case "paused":
		return "paused"
	case "enabled":
		return "enabled"
	case "":
		return "stopped"
	default:
		return strings.ToLower(state)
	}
}

// SubstrateKind identifies which deployment substrate a DeploymentStatus row came
// from — the discriminator that lets `charly status` present pod, VM, kubernetes, local,
// and android deployments side-by-side from one unified table. The command:status
// plugin (fan-out + render) and every substrate plugin's status-collect Op share
// this type; #DeploymentStatus.kind references it via @go(Kind,type=SubstrateKind).
//
// HAND-WRITTEN — a distinct named string type with the substrate consts, NOT emitted
// by `task cue:gen` (gengotypes references it from the generated DeploymentStatus.Kind
// field but does not define it; a CUE string enum would degrade to a plain string and
// lose the typed consts the collectors switch on).
type SubstrateKind string

const (
	SubstratePod        SubstrateKind = "pod"
	SubstrateVM         SubstrateKind = "vm"
	SubstrateKubernetes SubstrateKind = "kubernetes"
	SubstrateLocal      SubstrateKind = "local"
	SubstrateAndroid    SubstrateKind = "android"
)
