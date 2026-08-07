package check

// members.go — K1-unblock W3 Unit A: cross-deployment probing (${HOST:<member>} + `on:` driver
// resolution), relocated from charly/check_members.go. Same "library, not yet wired" status as
// venue.go — see that file's header for the Unit A/Unit B staging note.
//
// Two of the original's helpers (resolveDeployBoxName/resolveImageRefForEnsure) re-derived a LIVE
// deployment's image ref from the project (deploy key → box name → registry ref). They are DELETED:
// a live deployment's image identity is the image its container is RUNNING, read off the container
// by live_image.go's liveDeployMetadata — see that file for the defect the re-derivation caused.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolveHostVarsForChecks scans the given checks for ${HOST:<member>} references, resolves each,
// and returns the resolved address map plus the teardown funcs for any ssh -L forwards opened.
func resolveHostVarsForChecks(ex *sdk.Executor, ctx context.Context, dir string, checks []spec.Op, instance string) (map[string]string, []func()) {
	refs := kit.CollectHostRefs(checks)
	if len(refs) == 0 {
		return nil, nil
	}
	return resolveHostVars(ex, ctx, dir, refs, instance)
}

// resolveHostVarsForSteps is the plan-step counterpart, flattening every step's embedded Op.
func resolveHostVarsForSteps(ex *sdk.Executor, ctx context.Context, dir string, plan []spec.Step, instance string) (map[string]string, []func()) {
	checks := make([]spec.Op, 0, len(plan))
	for _, st := range plan {
		checks = append(checks, st.Op)
	}
	return resolveHostVarsForChecks(ex, ctx, dir, checks, instance)
}

// resolveHostVars resolves each ${HOST:<member>} key to its address. A key that can't be
// resolved is left OUT of the map; the referencing check then FAILS (an unreachable member is a
// real failure, never a silent skip). Returns cleanups for any ssh -L forwards opened.
func resolveHostVars(ex *sdk.Executor, ctx context.Context, dir string, refs []string, instance string) (map[string]string, []func()) {
	vars := map[string]string{}
	var cleanups []func()
	for _, key := range refs {
		_, arg, ok := kit.SplitHostKey(key)
		if !ok {
			continue
		}
		dep, portStr, hasPort := strings.Cut(arg, ":")
		if !hasPort {
			if _, ctr, err := deploykit.ResolveContainer(arg, instance); err == nil {
				vars[key] = ctr
			} else {
				fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, err)
			}
			continue
		}
		port, perr := strconv.Atoi(strings.TrimSpace(portStr))
		if perr != nil || port < 1 || port > 65535 {
			fmt.Fprintf(os.Stderr, "check: ${%s} — invalid port %q\n", key, portStr)
			continue
		}
		venue, verr := resolveCheckVenue(ex, ctx, dir, dep, instance)
		if verr != nil {
			fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, verr)
			continue
		}
		ep, eerr := resolveCheckEndpoint(venue, port)
		if eerr != nil {
			fmt.Fprintf(os.Stderr, "check: ${%s} — %v\n", key, eerr)
			continue
		}
		vars[key] = ep.Addr
		cleanups = append(cleanups, ep.Close)
	}
	return vars, cleanups
}

// liveTargetResolver builds the `on:` DRIVER venue resolver used by `charly check live` (and
// kind:check beds).
func liveTargetResolver(ex *sdk.Executor, ctx context.Context, dir, instance string) func(string) (*kit.CheckVarResolver, deploykit.DeployExecutor, error) {
	return func(target string) (*kit.CheckVarResolver, deploykit.DeployExecutor, error) {
		venue, err := resolveCheckVenue(ex, ctx, dir, target, instance)
		if err != nil {
			return nil, nil, err
		}
		res := liveDeployVarResolver(ex, ctx, target, instance, venue)
		return res, venue.Exec, nil
	}
}

// liveDeployVarResolver builds a runtime var resolver for a named pod deployment (container
// venue). Best-effort: a non-container venue or an unreadable image label yields an empty
// resolver. It takes no project dir: the deployment's image identity comes from the RUNNING
// container, so nothing here reads the resolved-project envelope any more.
func liveDeployVarResolver(ex *sdk.Executor, ctx context.Context, name, instance string, venue *CheckVenue) *kit.CheckVarResolver {
	if venue == nil || !venue.IsContainer() {
		return &kit.CheckVarResolver{}
	}
	var deployOverlay *spec.FleetNode
	if dc, derr := loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex); derr == nil && dc != nil {
		if entry, ok := dc.Fleet[spec.DeployKey(name, instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Fleet[name]; ok {
			deployOverlay = &entry
		}
	}
	// The driver's runtime vars are read off the image the venue container is RUNNING
	// (live_image.go) — the same single image identity the plan gather uses.
	meta, err := liveDeployMetadata(venue.Engine, venue.Name)
	if err != nil || meta == nil {
		return &kit.CheckVarResolver{}
	}
	res, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, venue.Engine, name, venue.Name, instance)
	return stampCharlyBin(res)
}

// stampCharlyBin records the active charly executable path into a runtime check-var resolver's
// Env as CHARLY_BIN — ported unchanged (no core dependency; os.Executable() works from any
// process).
func stampCharlyBin(res *kit.CheckVarResolver) *kit.CheckVarResolver {
	if res == nil {
		return res
	}
	if res.Env == nil {
		res.Env = map[string]string{}
	}
	if path, err := os.Executable(); err == nil && strings.TrimSpace(path) != "" {
		res.Env["CHARLY_BIN"] = path
	}
	return res
}
