package check

// members.go — K1-unblock W3 Unit A: cross-deployment probing (${HOST:<member>} + `on:` driver
// resolution), relocated from charly/check_members.go. Same "library, not yet wired" status as
// venue.go — see that file's header for the Unit A/Unit B staging note.
//
// Two of the original's helpers (resolveDeployBoxName/resolveImageRefForEnsure) re-resolved a
// deploy's image ref via a fresh LoadUnified + ResolveBox call; here they read the SAME data off
// the already-fetched resolved-project envelope (rp.Deploy[key].Image, rp.Boxes[bareRef]) —
// exactly the pattern k8s_config.go's findK8sSpec (K1-unblock wave 2) already established: the
// envelope already carries what a fresh core-side resolve would recompute, so no new mechanism,
// no second HostBuild round-trip.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
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
		res := liveDeployVarResolver(ex, ctx, dir, target, instance, venue)
		return res, venue.Exec, nil
	}
}

// liveDeployVarResolver builds a runtime var resolver for a named pod deployment (container
// venue). Best-effort: a non-container venue or an unreadable image label yields an empty
// resolver.
func liveDeployVarResolver(ex *sdk.Executor, ctx context.Context, dir, name, instance string, venue *CheckVenue) *kit.CheckVarResolver {
	if venue == nil || !venue.IsContainer() {
		return &kit.CheckVarResolver{}
	}
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil || rp == nil {
		return &kit.CheckVarResolver{}
	}
	var deployOverlay *spec.BundleNode
	if dc := deploykit.LoadDeployConfigForRead("charly check live on:"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(name, instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[name]; ok {
			deployOverlay = &entry
		}
	}
	imageRef := resolveDeployBoxName(rp, name, instance)
	resolvedRef, err := resolveImageRefForEnsure(rp, imageRef)
	if err != nil {
		return &kit.CheckVarResolver{}
	}
	meta, err := deploykit.ExtractMetadata(venue.Engine, resolvedRef)
	if err != nil || meta == nil {
		return &kit.CheckVarResolver{}
	}
	res, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, venue.Engine, name, venue.Name, instance)
	return stampCharlyBin(res)
}

// resolveDeployBoxName maps a deploy-key name to the box/image it deploys, off the
// resolved-project envelope's merged deploy tree (rp.Deploy[key].Image already carries the
// user-overlay-wins-over-project value the core original's two-step LoadDeployConfigForRead +
// LoadUnified lookup recomputed) — falling back to the key itself (the key==image convention),
// matching charly/deploy.go's resolveDeployBoxName exactly.
func resolveDeployBoxName(rp *spec.ResolvedProject, key, instance string) string {
	if key == "" {
		return key
	}
	if dc := deploykit.LoadDeployConfigForRead("resolveDeployBoxName"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(key, instance)]; ok && entry.Image != "" {
			return entry.Image
		}
		if entry, ok := dc.Bundle[key]; ok && entry.Image != "" {
			return entry.Image
		}
	}
	if rp != nil {
		if node, ok := rp.Deploy[key]; ok && node != nil && node.Image != "" {
			return node.Image
		}
	}
	return key
}

// resolveImageRefForEnsure converts a user-authored image identifier into a fully-qualified
// registry ref, off the envelope's already-resolved rp.Boxes[bareRef] view instead of a fresh
// ResolveBox call (which needs core's *Config — unavailable here).
func resolveImageRefForEnsure(rp *spec.ResolvedProject, image string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("empty image")
	}
	stripped := kit.StripURLScheme(image)
	if spec.IsRemoteImageRef(stripped) {
		return image, nil
	}
	if kit.LooksLikeFullRef(image) {
		return image, nil
	}
	if rp == nil {
		return "", fmt.Errorf("short name %q requires a resolved project", image)
	}
	view, ok := rp.Boxes[image]
	if !ok {
		return "", fmt.Errorf("resolving %q: not found in the resolved project", image)
	}
	return kit.ResolveShellImageRef(view.Registry, view.Name, ""), nil
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
