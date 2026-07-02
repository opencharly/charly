package sdk

import (
	"encoding/json"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/spec"
)

// deploy.go — the out-of-tree DEPLOY-plugin helpers. An external deploy provider
// receives the host's InstallPlans + venue descriptor on an OpExecute Invoke,
// applies its effects on the venue via the E3b reverse channel (sdk.Executor),
// and RETURNS a DeployReply carrying the teardown ops + a ledger record. These
// helpers decode the inputs and build the reply with the spec wire types — so a
// plugin never hand-rolls the JSON, and the SAME spec.ReverseOp / DeployReply
// the host records is the one the plugin constructs (R3, one shared type).
//
// Shared with the future external step/builder cutovers: the plan + reverse-op
// wire is the same envelope.

// DecodeInstallPlans decodes the host-marshalled InstallPlan VIEWS carried in an
// OpExecute Invoke's params_json (op.Params). Returns nil for an empty payload
// (a deploy with no candy plans — e.g. a marker-only example). The rich in-core
// Steps are NOT on the wire (see spec.InstallPlanView); the provenance fields
// prove the plan travelled.
func DecodeInstallPlans(paramsJSON []byte) ([]spec.InstallPlanView, error) {
	if len(paramsJSON) == 0 {
		return nil, nil
	}
	var plans []spec.InstallPlanView
	if err := json.Unmarshal(paramsJSON, &plans); err != nil {
		return nil, err
	}
	return plans, nil
}

// DecodeDeployVenue decodes the venue descriptor the host put in an OpExecute
// Invoke's env_json (op.Env). The zero DeployVenue is returned for an empty
// payload (the common "no venue env" call, e.g. the e2e direct invoke).
func DecodeDeployVenue(envJSON []byte) (spec.DeployVenue, error) {
	var v spec.DeployVenue
	if len(envJSON) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(envJSON, &v); err != nil {
		return v, err
	}
	return v, nil
}

// PluginScriptReverseOp builds the generic recordable teardown op an external
// deploy/step/builder plugin returns: a verbatim shell script run at `charly
// bundle del` time at the given scope (spec.ScopeSystem → root, spec.ScopeUser →
// deploy user). The host records it in the ledger and replays it via
// reverse_ops.go's reversePluginScript — record-and-replay, never recomputed.
func PluginScriptReverseOp(scope spec.Scope, script string) spec.ReverseOp {
	return spec.ReverseOp{
		Kind:  spec.ReverseOpPluginScript,
		Scope: scope,
		Extra: map[string]string{spec.ReverseOpPluginScriptKey: script},
	}
}

// BuildDeployReply assembles the OpExecute reply: the teardown ops the host
// records + the ledger CandyRecord identity (candy name + version). The host
// decodes the same spec.DeployReply and persists it via install_ledger.go. A
// plugin's Invoke returns this directly as its *pb.InvokeReply.
func BuildDeployReply(reverseOps []spec.ReverseOp, candy, version string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(spec.DeployReply{
		ReverseOps: reverseOps,
		Record:     spec.DeployReplyRecord{Candy: candy, Version: version},
	})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}
