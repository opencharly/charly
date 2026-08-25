package dsh

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencharly/plugin-dsh/candy/plugin-dsh/params"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// verb.go is the `dsh:` check VERB — the declarative in-box probe counterpart of
// the `charly dsh` command plugin. It is IN-BOX (the one place plugin-dsh
// diverges from plugin-agentteams' host-based cc.ResolveEndpoint): the provider
// runs dsh commands INSIDE the venue via cc.Exec().RunCapture — `dsh --version`,
// `ls $DSH_HOME/profiles`, `dsh plugin --profile <name> list` — and probes the
// web UI on 127.0.0.1:3080 in-box (the loopback-bound web app is unreachable from
// the host except through the socat forwarder, and the in-box probe is the direct
// check). cc.Exec() works under BOTH modes: in-box under CheckModeLive (the
// running deployment) and in a disposable container under CheckModeBox (the built
// image — which carries the dsh binary + $DSH_HOME layout). Only web-running
// skips under box mode (no running service on a disposable `podman run --rm`).
// The verb's method + method-exclusive modifiers ride the desugared plugin input
// (params.DshInput — the generated #DshInput from schema/dsh.cue), validated at
// runtime against the served schema.

// runVerbDsh is the verb dispatch: run the method in-box and return its output
// for the shared verdict pipeline (sdk.VerbVerdict grades it against the authored
// op matchers).
func runVerbDsh(ctx context.Context, cc kit.CheckContext, op *spec.Op, in params.DshInput) (string, error) {
	switch in.Method {
	case "version":
		return verbVersion(ctx, cc)
	case "web-running":
		return verbWebRunning(ctx, cc)
	case "profile-list":
		return verbProfileList(ctx, cc)
	case "plugin-list":
		return verbPluginList(ctx, cc, in.Profile)
	default:
		return "", fmt.Errorf("unknown dsh method %q", in.Method)
	}
}

// verbVersion runs `dsh --version` in the venue and returns the version string.
// Works under both box and live modes (the built image carries the dsh binary).
func verbVersion(ctx context.Context, cc kit.CheckContext) (string, error) {
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, "dsh --version")
	if err != nil {
		return "", fmt.Errorf("run dsh --version: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("dsh --version exited %d: %s", exit, strings.TrimSpace(stderr))
	}
	return "dsh " + strings.TrimSpace(stdout), nil
}

// verbWebRunning probes the dsh web UI on 127.0.0.1:3080 IN-BOX via curl — the
// direct check of the loopback-bound web app (the socat forwarder is what makes
// it reachable from the host; the in-box probe verifies the app itself). curl -f
// fails on any non-2xx, so exit 0 means the web UI answers. Skips under box mode
// (no running service on a disposable container) — the RunVerb / invokeVerb
// dispatch gates that.
func verbWebRunning(ctx context.Context, cc kit.CheckContext) (string, error) {
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, "curl -fsS -o /dev/null http://127.0.0.1:3080/")
	if err != nil {
		return "", fmt.Errorf("probe dsh web UI: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("dsh web UI not answering on 127.0.0.1:3080: %s", strings.TrimSpace(stderr))
	}
	_ = stdout
	return "dsh web UI answers HTTP 200 on 127.0.0.1:3080", nil
}

// verbProfileList lists the profiles under $DSH_HOME/profiles in the venue.
// DSH_HOME defaults to ~/.dsh (the dsh candy's env); the profile directory may
// not exist yet on a fresh box (the web app creates profiles on first run), so a
// missing directory reports "(no profiles yet)" instead of failing.
func verbProfileList(ctx context.Context, cc kit.CheckContext) (string, error) {
	script := `profiles="${DSH_HOME:-$HOME/.dsh}/profiles"; if [ -d "$profiles" ]; then ls -1 "$profiles"; else echo "(no profiles yet)"; fi`
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, script)
	if err != nil {
		return "", fmt.Errorf("list dsh profiles: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("list dsh profiles exited %d: %s", exit, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}

// verbPluginList lists the plugins for a profile in the venue via `dsh plugin
// --profile <name> list` (the CLI forwards `list` to pnpm in the profile
// directory). The profile defaults to "web" — the profile the dsh candy's service
// boots.
func verbPluginList(ctx context.Context, cc kit.CheckContext, profile string) (string, error) {
	if profile == "" {
		profile = "web"
	}
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, "dsh plugin --profile "+profile+" list")
	if err != nil {
		return "", fmt.Errorf("list dsh plugins for profile %s: %w", profile, err)
	}
	if exit != 0 {
		return "", fmt.Errorf("dsh plugin list for profile %s exited %d: %s", profile, exit, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}
