package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// host_build_deploy_artifacts_retrieve.go — the "deploy-artifacts-retrieve" F10 host-builder
// (Cone A shape 3): the genuine floor-M half of the former core-resident retrieveArtifactsAndK3s
// (charly's own deleted deploy_add_shared.go) — re-scanning the project for the deploy's candies
// (CandyForPlan, same ScanAllCandyWithConfig coupling as the sibling deploy-candy-secrets seam)
// and pulling back each one's declared `artifacts:` via deploykit.RetrieveCandyArtifacts over the
// deploy's OWN venue executor (re-materialized from venue_json — the SAME kit.VenueFromDescriptor
// conversion every other venue-consuming seam uses). Runs AFTER the substrate dispatch succeeds
// (the venue must already exist) — candy/plugin-bundle's handleDeployApply calls this once, Add
// only. The register-hint-driven k3s-post-provision DISPATCH itself is NOT here: that decision +
// the verb:kube InvokeProvider call happen plugin-side, using the register_hints the sibling
// "deploy-candy-secrets" seam already returned (one candy scan feeds both — R3).
const deployArtifactsRetrieveBuilderKind = "deploy-artifacts-retrieve"

func hostBuildDeployArtifactsRetrieve(ctx context.Context, req spec.DeployArtifactsRetrieveRequest, _ buildEngineContext) (spec.DeployArtifactsRetrieveReply, error) {
	plans, err := decodePlanViews(req.PlansJSON)
	if err != nil {
		return spec.DeployArtifactsRetrieveReply{}, fmt.Errorf("deploy-artifacts-retrieve: %w", err)
	}
	candyList, err := CandyForPlan(plans, req.Dir, nil)
	if err != nil {
		return spec.DeployArtifactsRetrieveReply{}, err
	}

	var exec deploykit.DeployExecutor
	if len(req.VenueJSON) > 0 {
		var d spec.VenueDescriptor
		if derr := json.Unmarshal(req.VenueJSON, &d); derr != nil {
			return spec.DeployArtifactsRetrieveReply{}, fmt.Errorf("deploy-artifacts-retrieve: decode venue descriptor: %w", derr)
		}
		e, verr := kit.VenueFromDescriptor(d)
		if verr != nil {
			return spec.DeployArtifactsRetrieveReply{}, fmt.Errorf("deploy-artifacts-retrieve: materialize venue: %w", verr)
		}
		exec = e
	}
	if exec == nil {
		exec = kit.ShellExecutor{}
	}

	if err := deploykit.RetrieveCandyArtifacts(ctx, exec, candyList, kit.SanitizeDeployName(req.ArtifactKey), req.ArtifactEnv, spec.EmitOpts{}, loadedReadiness()); err != nil {
		return spec.DeployArtifactsRetrieveReply{}, err
	}
	return spec.DeployArtifactsRetrieveReply{}, nil
}

var _ = func() bool {
	registerHostBuilder(deployArtifactsRetrieveBuilderKind, typedHostBuilder(deployArtifactsRetrieveBuilderKind, hostBuildDeployArtifactsRetrieve))
	return true
}()
