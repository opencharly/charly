package check

// bed_persist.go — the bed-root + member deploy-override PERSIST, plugin-side (#55 coneC-dsh β1).
// The former host-side wrapper (charly/check_bed_run.go's persistBedDeployOverrides) + its
// deploykit import shed from charly core; the host "check-bed" setup seam now threads the bed-root
// FleetNode (with nested peer Members) as spec.CheckBedReply.NodeJSON, and this helper calls
// deploykit.PersistBedDeployOverrides itself — supplying its OWN loader-threaded marshalNode +
// reader (the deployMarshalNode/deployConfigReader pattern candy/plugin-deploy-pod +
// candy/plugin-fleet already use over the PERMANENT HostBuild("loader-threaded") leg), so the
// write no longer depends on the charly-init DeployStateHost package var nor the K1-tied
// marshalDeployNode host callback. Mirrors candy/plugin-deploy-pod/deploy_save_state.go (R3).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// bedFetchLoaderPrimaries returns the loader-threaded Primaries DATA snapshot (plugin-verb WORD →
// scalar-sugar primary field) via the generic "loader-threaded" host builder — the SAME map
// candy/plugin-deploy-pod's fetchLoaderPrimaries resolves. deploykit.MarshalFleetNode resugars
// each plan step with it. A HostBuild failure degrades to an empty map (a plan with no plugin-verb
// sugar marshals identically).
func bedFetchLoaderPrimaries(ctx context.Context, ex *sdk.Executor) map[string]string {
	out, err := ex.HostBuild(ctx, "loader-threaded", nil)
	if err != nil {
		return nil
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	return t.Primaries
}

// bedMarshalNode builds the per-entry node-form marshal callback deploykit.PersistBedDeployOverrides
// takes. It resugars each plan step via the loader-threaded Primaries snapshot — the SAME
// registry-derived D-fact the former host marshalDeployNode fed to deploykit.MarshalFleetNode via
// loaderThreaded().Primaries. Sourcing Primaries PLUGIN-SIDE is what lets the bed persist run here
// instead of over a host seam.
func bedMarshalNode(ctx context.Context, ex *sdk.Executor) func(name string, node *deploykit.FleetNode) (*yaml.Node, error) {
	primaries := bedFetchLoaderPrimaries(ctx, ex)
	return func(_ string, node *deploykit.FleetNode) (*yaml.Node, error) {
		return deploykit.MarshalFleetNode(node, primaries)
	}
}

// bedConfigReader is the loader-backed reader callback deploykit.PersistBedDeployOverrides takes for
// its load-mutate-save — the cycle-free loaderkit.LoadHostFleetConfigViaExecutor read, so the write
// needs no charly-init DeployStateHost.
func bedConfigReader(ctx context.Context, ex *sdk.Executor) func() (*deploykit.FleetConfig, error) {
	return func() (*deploykit.FleetConfig, error) { return loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex) }
}

// persistBedDeployOverridesPluginSide seeds the per-host charly.yml with the bed ROOT's + each
// MEMBER's project-declared deploy-shaped overrides (port / volume / env / security / network + the
// resource-arbitration role) PLUGIN-SIDE, replacing the former host-side persistBedDeployOverrides
// wrapper. The bed-root FleetNode (with nested peer Members) arrives as d.NodeJSON; the root
// persist is guarded by !d.IsVM (matching the former host guard — a VM bed runs no `charly config`)
// and passes d.IsExternal as externalInPlace (bed_session.go's bedSetup computes it via
// spec.ExternalInPlaceVenue, #55 W3 B2-full — no more host registry round-trip). Each member is
// persisted from the root's nested peer map, with externalInPlace derived the SAME way
// (spec.ExternalInPlaceVenue, R3 — one shared predicate, no third copy). deploykit.
// PersistBedDeployOverrides internally self-skips a group root (IsGroup), a local/host-rooted
// node, and an in-place external node — so calling it unconditionally for the root + members is
// safe + matches the former host behavior. Best-effort (stderr warnings, no error return) —
// matching the former host wrapper (a persist failure does not abort the bed run; the bed's own
// `charly config` re-saves the overlay).
func persistBedDeployOverridePluginSide(ctx context.Context, ex *sdk.Executor, name string, d spec.CheckBedReply) {
	if len(d.NodeJSON) == 0 {
		return // no bed-root threaded (e.g. a degraded host) — nothing to persist
	}
	var root spec.Deploy
	if err := json.Unmarshal(d.NodeJSON, &root); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: check-bed %s: decode bed root for persist: %v\n", name, err)
		return
	}
	marshalNode := bedMarshalNode(ctx, ex)
	reader := bedConfigReader(ctx, ex)
	// Root persist — guarded !IsVM (a VM bed runs no `charly config`); the deploykit func self-skips
	// group/local/external-in-place.
	if !d.IsVM {
		deploykit.PersistBedDeployOverrides(name, deploykit.FleetNode(root), d.IsExternal, marshalNode, reader)
	}
	// Member persist — each peer member from the root's nested map, BEFORE members-up
	// runs the member's `charly config`/`charly start`. A member's externalInPlace is derivable from
	// its stamped Descent (spec.ExternalInPlaceVenue). Mirrors the former bringUpMembers per-member
	// persist.
	for _, memberKey := range spec.SortedMemberKeys(root.Members) {
		member := root.Members[memberKey]
		if member == nil {
			continue
		}
		deploykit.PersistBedDeployOverrides(memberKey, *member, spec.ExternalInPlaceVenue(member), marshalNode, reader)
	}
}
