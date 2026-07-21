package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_entity_resolve.go — the "deploy-entity-resolve" F10 host-builder (FINAL/K5
// unit 6a): the GENERIC LoadUnified-coupled lookup an F6 substrate preresolve body needs but
// cannot do itself (a separate-module plugin cannot import LoadUnified — a kernel Mechanism, R-E2
// stands: it never moves wholesale). `kind` is DATA the switch below dispatches on (clause-D) —
// adding a fifth substrate is a new `case`, never a new HostBuild kind or wire shape.
const deployEntityResolveBuilderKind = "deploy-entity-resolve"

func hostBuildDeployEntityResolve(_ context.Context, req spec.DeployEntityResolveRequest, _ buildEngineContext) (spec.DeployEntityResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	switch req.Kind {
	case "", "deploy", "bundle":
		// "bundle" is the unit-6b-facing alias (candy/plugin-kube's k3s_post.go
		// deployVMForwards, S3, resolves an entityRef that may be a bundle key, needing the
		// node's From field for one hop into a "vm" lookup) — same lookup as the
		// default/"deploy" case, no separate mechanism.
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: resolve deploy tree: %w", err)
		}
		n, ok := tree[req.Name]
		if !ok {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no deploy entry %q", req.Name)
		}
		return spec.DeployEntityResolveReply{Node: &n}, nil
	case "k8s":
		spc := findK8sSpec(dir, req.Name)
		if spc == nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: cluster %q not declared in the k8s: section", req.Name)
		}
		b, err := json.Marshal(spc)
		if err != nil {
			return spec.DeployEntityResolveReply{}, err
		}
		return spec.DeployEntityResolveReply{EntityJSON: b}, nil
	case "android":
		spc := findAndroidSpec(dir, req.Name)
		if spc == nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: kind:android device %q not declared in the android: section", req.Name)
		}
		specJSON, err := json.Marshal(spc)
		if err != nil {
			return spec.DeployEntityResolveReply{}, err
		}
		email, token := resolveAndroidGoogleCreds(spc.GoogleAccount)
		b, err := json.Marshal(spec.AndroidEntityResolution{SpecJSON: specJSON, GoogleEmail: email, GoogleToken: token})
		if err != nil {
			return spec.DeployEntityResolveReply{}, err
		}
		return spec.DeployEntityResolveReply{EntityJSON: b}, nil
	case "vm":
		uf, ok, err := LoadUnified(dir)
		if err != nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: loading charly.yml: %w", err)
		}
		if !ok || uf.VM == nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no charly.yml or no kind:vm entities declared")
		}
		body, ok := uf.VM[req.Name]
		if !ok {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no kind:vm entity named %q", req.Name)
		}
		vmSpec, err := resolveVmViaPlugin(body)
		if err != nil {
			return spec.DeployEntityResolveReply{}, err
		}
		if vmSpec == nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: kind:vm entity %q resolved to an empty value", req.Name)
		}
		b, err := json.Marshal(vmSpec)
		if err != nil {
			return spec.DeployEntityResolveReply{}, err
		}
		return spec.DeployEntityResolveReply{EntityJSON: b}, nil
	default:
		return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: unknown kind %q", req.Kind)
	}
}

var _ = func() bool {
	registerHostBuilder(deployEntityResolveBuilderKind, typedHostBuilder(deployEntityResolveBuilderKind, hostBuildDeployEntityResolve))
	return true
}()

// findAndroidSpec resolves a kind:android device by name from the unified config. Relocated from
// the deleted android_deploy_cmd.go (FINAL/K5 unit 6a) — LoadUnified-coupled, stays core behind
// the "deploy-entity-resolve" seam.
func findAndroidSpec(dir, name string) *ResolvedAndroid {
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf == nil || uf.Android == nil {
		return nil
	}
	return lookupAndroidSpec(uf, name)
}

// lookupAndroidSpec resolves a kind:android device by name from the unified config (K5: relocated
// from the deleted status_collect_adb.go originally — the status collector's own android lookup
// lives in candy/plugin-substrate/status_android_collect.go's androidSpecFor, a SEPARATE
// plugin-local copy since a plugin cannot import charly/ types; this copy now serves ONLY the
// "deploy-entity-resolve" seam).
func lookupAndroidSpec(uf *UnifiedFile, name string) *ResolvedAndroid {
	if uf == nil || uf.Android == nil || name == "" {
		return nil
	}
	body, ok := uf.Android[name]
	if !ok {
		return nil
	}
	r, err := resolveAndroidViaPlugin(body)
	if err != nil {
		return nil
	}
	return r
}

// resolveAndroidGoogleCreds reads the apkeep google-play credentials from the credential store
// using the device's google_account secret-key refs (or the GOOGLE_ACCOUNT_EMAIL /
// GOOGLE_AAS_TOKEN defaults). Empty when unset. Relocated from the deleted
// android_deploy_cmd.go (FINAL/K5 unit 6a): the credential-STORE touch (DefaultCredentialStore →
// verb:credential) is core-only, so this ONE small piece stays behind the
// "deploy-entity-resolve" seam while the REST of android_deploy_cmd.go's device-resolution logic
// (container/engine-inspect based, no LoadUnified/credential coupling) moved to
// candy/plugin-adb/preresolve.go.
func resolveAndroidGoogleCreds(ga *AndroidGoogleAccount) (email, token string) {
	emailKey, tokenKey := "GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN"
	if ga != nil {
		if ga.EmailSecret != "" {
			emailKey = ga.EmailSecret
		}
		if ga.TokenSecret != "" {
			tokenKey = ga.TokenSecret
		}
	}
	store := DefaultCredentialStore()
	email, _ = store.Get("charly/secret", emailKey)
	token, _ = store.Get("charly/secret", tokenKey)
	return email, token
}
