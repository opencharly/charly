package pod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// enc_cmd.go — the `charly config status|mount|unmount|passwd` leaf BODIES (wave γ port from
// charly/enc.go + charly/config_image.go). These four leaves were the LAST CLI-invoked commands
// still forwarding to core over a HostBuild("pod-config-<leaf>") seam purely to reach the
// core-only credential/enc registry APIs (providerRegistry.resolve + connectPluginByWord).
//
// The port applies the ALREADY-LIVE pattern candy/plugin-deploy-pod/lifecycle.go proves for the
// start/stop path: this plugin already holds a real reverse-channel *sdk.Executor (cmdExec,
// stashed by Invoke(OpRun) — see host_seams.go) with InvokeProvider capability, so verb:enc and
// verb:credential are dispatched DIRECTLY, no core round-trip needed. The passphrase-resolution
// ORCHESTRATION (sdk/deploykit/enc_passphrase.go) was already portable, injected-CredentialAccess
// shaped — only the credential-store BACKEND (previously charly-core's coreCredentialAccess,
// itself forwarding through credential_plugin.go's core-only pluginCredentialStore) needed a
// plugin-side equivalent. credentialInput/credentialReply below are byte-compatible wire copies
// of the SAME verb:credential contract charly/credential_plugin.go and candy/plugin-secrets/
// verb_credential.go already each keep their own copy of (documented there as intentional:
// process-boundary wire shapes are not worth a cross-module import for a 3-field struct) — this
// is a third, not a new, instance of that established pattern.
//
// Setup/Remove (ConfigSetupCmd/ConfigRemoveCmd, pod_cmd.go) stay on hostPodSeam — their
// orchestration is a different family (quadlet/secrets/sidecars, P13-KERNEL, host_build_pod_
// config_seams.go), not credential/enc.

// credentialInput is the verb:credential request wire form — byte-compatible with
// charly/credential_plugin.go's credentialInput and candy/plugin-secrets/params.CredentialInput
// (CUE-sourced there; this plugin keeps its own small hand copy rather than importing another
// candy's module, mirroring the codebase's existing cross-boundary-wire-shape convention).
type credentialInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
}

// credentialReply is the verb:credential reply wire form actually consumed by the enc leaves
// (value/source/error — the subset this file needs; the fuller shape with keys/name/health lives
// in charly/credential_plugin.go's copy for the CLI's other credential-store operations).
type credentialReply struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

// pluginCredentialCall dispatches one verb:credential operation over the stashed reverse-channel
// executor with the command's own context (mirrors charly/credential_plugin.go's
// pluginCredentialStore.call, minus the core-only registry connect).
func pluginCredentialCall(in credentialInput) (credentialReply, error) {
	return pluginCredentialCallCtx(cmdCtx, in)
}

// pluginCredentialCallCtx is pluginCredentialCall's ctx-carrying sibling — the blocking
// `await-unlock` method needs the SIGINT/SIGTERM-cancellable ctx, exactly as
// pluginCredentialStore.callCtx did in core.
func pluginCredentialCallCtx(ctx context.Context, in credentialInput) (credentialReply, error) {
	var reply credentialReply
	if cmdExec == nil {
		return reply, fmt.Errorf("pod config credential: no host reverse channel (command not compiled-in?)")
	}
	inJSON, err := json.Marshal(in)
	if err != nil {
		return reply, err
	}
	resJSON, err := cmdExec.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return reply, fmt.Errorf("credential invoke: %w", err)
	}
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return reply, uerr
		}
	}
	return reply, nil
}

// pluginResolveCredential mirrors charly/credential_plugin.go's ResolveCredential's store-branch
// (the enc call sites here always pass envVar="", so the env-override branch lives in
// pluginCredentialAccess.Resolve, one layer up, exactly where ResolveCredential kept it).
func pluginResolveCredential(service, key, defaultVal string) (value, source string) {
	r, err := pluginCredentialCall(credentialInput{Method: "resolve", Service: service, Key: key})
	if err != nil {
		return defaultVal, "unavailable"
	}
	if r.Value != "" {
		return r.Value, r.Source
	}
	src := r.Source
	if src == "" {
		src = "default"
	}
	return defaultVal, src
}

// pluginCredentialWrite persists one credential — the deploykit.CredentialAccess.Write half.
func pluginCredentialWrite(service, key, value string) error {
	r, err := pluginCredentialCall(credentialInput{Method: "set", Service: service, Key: key, Value: value})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// pluginCredentialAccess bundles the two RPC-backed operations into the deploykit.CredentialAccess
// shape sdk/deploykit/enc_passphrase.go's orchestration needs — the direct plugin-side analogue of
// charly/enc.go's former coreCredentialAccess().
func pluginCredentialAccess() deploykit.CredentialAccess {
	return deploykit.CredentialAccess{
		Resolve: func(envVar, service, key, defaultVal string) (string, string) {
			if envVar != "" {
				if v := os.Getenv(envVar); v != "" {
					return v, "env"
				}
			}
			return pluginResolveCredential(service, key, defaultVal)
		},
		Write: pluginCredentialWrite,
	}
}

// pluginAwaitKeyringUnlock blocks (via verb:credential `await-unlock`, RPC'd to
// candy/plugin-secrets) until the keyring unlocks or ctx is cancelled — the direct plugin-side
// analogue of charly/enc.go's former awaitKeyringUnlockViaPlugin. The resolver/reset closures are
// unused here for the same reason the core version left them unused: the RPC target re-probes its
// OWN store across the process boundary.
func pluginAwaitKeyringUnlock(ctx context.Context, boxName string, _ func() (string, string), _ func()) (string, string, error) {
	r, err := pluginCredentialCallCtx(ctx, credentialInput{Method: "await-unlock", Service: "charly/enc", Key: boxName})
	if err != nil {
		return "", "", err
	}
	if r.Error != "" {
		return "", "", errors.New(r.Error)
	}
	return r.Value, r.Source, nil
}

// pluginResetCredentialStore re-probes the keyring between unlock attempts — the direct
// plugin-side analogue of charly/enc.go's resetDefaultCredentialStore. Unlike the core version,
// this dispatches unconditionally rather than short-circuiting on an "already connected" check
// (that optimization used the core-only provider registry; the RPC is cheap and this path only
// runs during an active keyring-wait retry loop, never on the hot path — registered as a
// low-priority simplification, not a correctness gap).
func pluginResetCredentialStore() {
	_, _ = pluginCredentialCall(credentialInput{Method: "reset"})
}

// pluginResolveSecretBackend mirrors charly/credential_plugin.go's resolveSecretBackend — pure
// env + sdk/kit config read, zero host coupling, portable verbatim.
func pluginResolveSecretBackend() string {
	if v := os.Getenv("CHARLY_SECRET_BACKEND"); v != "" {
		return v
	}
	cfg, err := kit.LoadRuntimeConfig()
	if err != nil {
		return "auto"
	}
	if cfg.SecretBackend != "" {
		return cfg.SecretBackend
	}
	return "auto"
}

// pluginEncExec dispatches verb:enc's OpExecute over the stashed reverse-channel executor — the
// direct plugin-side analogue of charly/enc.go's encExecViaPlugin (which resolved verb:enc via
// the core-only providerRegistry). Preserves the exact fuse.conf preflight (fail fast, before any
// volume mounts partway) and the reply-carried Error surfacing.
func pluginEncExec(in spec.EncExecInput) error {
	if in.Method == spec.EncMethodMount || in.Method == spec.EncMethodEnsure {
		if !deploykit.FuseAllowOtherEnabled() {
			return fmt.Errorf("encrypted volumes require 'user_allow_other' in /etc/fuse.conf (gocryptfs -allow_other, for rootless-podman keep-id access) — enable it with: echo user_allow_other | sudo tee -a /etc/fuse.conf")
		}
	}
	if cmdExec == nil {
		return fmt.Errorf("pod config enc: no host reverse channel (command not compiled-in?)")
	}
	inJSON, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resJSON, err := cmdExec.InvokeProvider(cmdCtx, "verb", "enc", sdk.OpExecute, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return fmt.Errorf("enc invoke: %w", err)
	}
	var reply spec.EncExecReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return uerr
		}
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// loadPodBundleConfig fetches the per-host BundleConfig over the EXISTING
// "pod-config-load-bundle" seam (deploykit.LoadBundleConfigViaSeam) rather than letting
// EncPlanFor/EncStatus reach the placement-dependent bare deploykit.LoadBundleConfig()
// themselves — the same fix candy/plugin-pod/remove_orchestration.go's resolveSidecarNames
// already applies for the same latent-bug class (R3, no new seam invented). Safe today
// because command:config is compiled-in-only, but routing through the seam means a future
// out-of-process placement fails loudly (a real error) instead of silently degrading to
// "no encrypted volumes" — exactly the historical bug this class of fix exists to prevent.
func loadPodBundleConfig(caller string) (*deploykit.BundleConfig, error) {
	return deploykit.LoadBundleConfigViaSeam(cmdCtx, cmdExec, caller)
}

// pluginEncStatus prints encrypted-volume status — zero credential coupling (mirrors
// charly/enc.go's encStatus 1:1), routed through the seam-aware EncStatusFromConfig.
func pluginEncStatus(boxName, instance string) error {
	dc, err := loadPodBundleConfig("charly config status")
	if err != nil {
		return err
	}
	return deploykit.EncStatusFromConfig(dc, boxName, instance)
}

// pluginEncMount mounts encrypted volumes for an image (direct port of charly/enc.go's encMount).
// Fast path preserved BYTE-IDENTICAL: if every requested volume is already mounted, return nil
// without querying the credential store at all (keyring-resilient restart).
func pluginEncMount(boxName, instance, volume string) error {
	dc, err := loadPodBundleConfig("charly config mount")
	if err != nil {
		return err
	}
	plan, err := deploykit.EncPlanForConfig(dc, boxName, instance, volume, deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}

	requested := len(plan)
	mounted := 0
	for _, p := range plan {
		if p.Mounted {
			mounted++
		}
	}
	if requested > 0 && mounted == requested {
		fmt.Fprintf(os.Stderr, "All encrypted volumes for %s already mounted (%d/%d)\n", boxName, mounted, requested)
		return nil
	}

	passphrase, err := deploykit.ResolveEncPassphraseForMount(boxName, pluginResolveSecretBackend(), pluginCredentialAccess(), pluginResetCredentialStore, pluginAwaitKeyringUnlock)
	if err != nil {
		return err
	}
	return pluginEncExec(spec.EncExecInput{
		Method:     spec.EncMethodMount,
		ImageID:    "charly-" + boxName,
		BoxName:    boxName,
		Passphrase: passphrase,
		Volumes:    plan,
	})
}

// pluginEncUnmount unmounts encrypted volumes for an image (direct port of enc.go's encUnmount).
func pluginEncUnmount(boxName, instance, volume string) error {
	dc, err := loadPodBundleConfig("charly config unmount")
	if err != nil {
		return err
	}
	plan, err := deploykit.EncPlanForConfig(dc, boxName, instance, volume, deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}
	return pluginEncExec(spec.EncExecInput{
		Method:  spec.EncMethodUnmount,
		ImageID: "charly-" + boxName,
		BoxName: boxName,
		Volumes: plan,
	})
}

// pluginAskPassword resolves one `charly config passwd` prompt: an env var override takes
// priority over the interactive systemd-ask-password prompt deploykit.AskPassword drives —
// the SAME "1. Test/CI override" precedent deploykit.ResolveEncPassphrase's GOCRYPTFS_PASSWORD
// check already establishes for the mount path (enc_passphrase.go), extended here since
// encPasswd's triple prompt (old/new/confirm) had no such hook. Lets an R10 check bed exercise
// `charly config passwd` end-to-end non-interactively (GOCRYPTFS_OLD_PASSWORD +
// GOCRYPTFS_NEW_PASSWORD, the latter answering both the new-passphrase AND confirm prompts —
// correct automation behavior, since a real operator would type the identical value twice
// too). Interactive use (both env vars unset) is completely unaffected.
func pluginAskPassword(envVar, id, prompt string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return v, nil
	}
	return deploykit.AskPassword(id, prompt)
}

// pluginEncPasswd changes the gocryptfs password for all encrypted volumes of an image (direct
// port of enc.go's encPasswd, byte-identical mount-guard + triple-prompt behavior).
func pluginEncPasswd(boxName, instance string) error {
	dc, err := loadPodBundleConfig("charly config passwd")
	if err != nil {
		return err
	}
	plan, err := deploykit.EncPlanForConfig(dc, boxName, instance, "", deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return fmt.Errorf("image %q has no encrypted bind mounts", boxName)
	}
	for _, m := range plan {
		if m.Mounted {
			return fmt.Errorf("encrypted volume %q is still mounted; run 'charly config unmount %s' first", m.Name, boxName)
		}
	}

	volID := "charly-" + boxName

	oldPass, err := pluginAskPassword("GOCRYPTFS_OLD_PASSWORD", volID+"-old", "Current passphrase:")
	if err != nil {
		return err
	}
	newPass, err := pluginAskPassword("GOCRYPTFS_NEW_PASSWORD", volID+"-new", "New passphrase:")
	if err != nil {
		return err
	}
	confirmPass, err := pluginAskPassword("GOCRYPTFS_NEW_PASSWORD", volID+"-confirm", "Confirm new passphrase:")
	if err != nil {
		return err
	}
	if newPass != confirmPass {
		return fmt.Errorf("new passphrase and confirmation do not match")
	}

	return pluginEncExec(spec.EncExecInput{
		Method:  spec.EncMethodPasswd,
		ImageID: volID,
		BoxName: boxName,
		OldPass: oldPass,
		NewPass: newPass,
		Volumes: plan,
	})
}
