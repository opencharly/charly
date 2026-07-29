package adb

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// credential_shim.go — deploy-cone cutover 1: resolveGoogleCreds reaches verb:credential
// (candy/plugin-secrets) directly, peer-to-peer over InvokeProvider, instead of having the host
// pre-resolve the apkeep google-play credentials behind the "deploy-entity-resolve" seam. This is
// the SAME peer-to-peer pattern candy/plugin-vm already uses for verb:arbiter/verb:gpu/verb:egress
// (vm_arbiter_shim.go et al.) — the former "credential STORE touch is core-only" justification
// (charly/host_build_deploy_entity_resolve.go's deleted resolveAndroidGoogleCreds) was stale.

// credentialGetInput / credentialGetReply mirror the wire shape verb:credential's `get` method
// expects/returns — byte-compatible with candy/plugin-secrets/params.CredentialInput and
// charly/credential_plugin.go's credentialReply (the cross-module credential contract; no shared
// Go type exists yet, so every consumer defines its own JSON-tag-compatible mirror, matching how
// the core adapter itself does it).
type credentialGetInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
}

type credentialGetReply struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

const credentialService = "charly/secret"

// getCredential fetches one key from verb:credential's "get" method. Errors (including "no such
// key") resolve to an empty string — matching the former core resolveAndroidGoogleCreds, which
// also discarded the error (email/token are optional; apkeep runs unauthenticated without them).
func getCredential(ctx context.Context, exec *sdk.Executor, key string) string {
	if exec == nil || key == "" {
		return ""
	}
	params, err := json.Marshal(credentialGetInput{Method: "get", Service: credentialService, Key: key})
	if err != nil {
		return ""
	}
	out, err := exec.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return ""
	}
	var reply credentialGetReply
	if len(out) > 0 {
		_ = json.Unmarshal(out, &reply)
	}
	return reply.Value
}

// resolveGoogleCreds resolves the apkeep google-play credentials using the device's
// google_account secret-key refs (or the GOOGLE_ACCOUNT_EMAIL / GOOGLE_AAS_TOKEN defaults) —
// byte-identical selection logic to the former core resolveAndroidGoogleCreds.
func resolveGoogleCreds(ctx context.Context, exec *sdk.Executor, ga *spec.GoogleAccount) (email, token string) {
	emailKey, tokenKey := "GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN"
	if ga != nil {
		if ga.EmailSecret != "" {
			emailKey = ga.EmailSecret
		}
		if ga.TokenSecret != "" {
			tokenKey = ga.TokenSecret
		}
	}
	return getCredential(ctx, exec, emailKey), getCredential(ctx, exec, tokenKey)
}
