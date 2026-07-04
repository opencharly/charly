package secrets

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/sdk/proto"

	"github.com/opencharly/charly/candy/plugin-secrets/params"
)

// provider.go is the out-of-process verb:credential provider — charly's host
// pluginCredentialStore (charly/credential_plugin.go) dispatches a credential
// operation to it through the registry (ResolveVerb("credential") → this grpcProvider
// → Provider.Invoke) with a params.CredentialInput marshaled as params_json. Unlike a
// check verb, verb:credential carries NO CheckEnv and returns the credentialReply wire
// form (value/source/keys/name/error/health) the host decodes back into CredentialStore
// results.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is the gRPC entry point for the ONE gRPC-served capability this plugin
// advertises: verb:credential. command:secrets (`charly secrets …`) is NOT served over
// gRPC — it is dispatched by charly syscall.Exec'ing this binary in CLI mode (sdk.Main →
// cliMain), so it never reaches Invoke and is absent from Describe.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in params.CredentialInput
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return replyJSON(credentialReply{Error: fmt.Sprintf("plugin-secrets: decode credential input: %v", err)})
		}
	}
	return replyJSON(dispatchCredential(ctx, in))
}

// replyJSON marshals the credentialReply into the InvokeReply envelope the host decodes.
func replyJSON(reply credentialReply) (*pb.InvokeReply, error) {
	j, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}
