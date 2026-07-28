package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
)

// check_venue.go — the generic venue-EXEC helpers the host's check reverse-legs run tool commands
// through (best-effort availability probes + fire-and-forget actions on an already-resolved
// executor). Kind-BLIND: they take a deploykit.DeployExecutor and run a shell string, with zero
// venue classification.
//
// The venue CLASSIFIER + executor builder (resolveCheckVenue + checkVmTarget / checkLocalTarget /
// resolveLeafVenue + the CheckVenue type) RELOCATED to candy/plugin-check (venue.go) in the #118
// check broker-envelope-out cutover: classifying a check target by DEPLOY KIND is CHECK CAPABILITY
// LOGIC, not kernel fabric (the boundary-law self-test forbids a floor file switching on a kind). The
// host's floor reverse-legs now reach that plugin-side classifier over verb:check-resolve
// (check_venue_resolve.go) and re-materialize the returned generic spec.VenueDescriptor via the
// kind-blind kit.EndpointForVenue / kit.VenueFromDescriptor. The endpoint→address resolver
// (resolveCheckEndpoint) is likewise gone — its ONE mechanism, kit.EndpointForVenue, is called
// directly by each leg off the descriptor.

// venueRunSilent runs a command on the venue discarding output, returning an
// error on non-zero exit (availability probes + fire-and-forget actions).
func venueRunSilent(ex deploykit.DeployExecutor, script string) error {
	_, _, exit, err := ex.RunCapture(context.Background(), script)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("command exited %d", exit)
	}
	return nil
}

// venueHasTool reports whether `tool` is on PATH on the venue.
func venueHasTool(ex deploykit.DeployExecutor, tool string) bool {
	_, _, exit, err := ex.RunCapture(context.Background(), "command -v "+tool+" >/dev/null 2>&1")
	return err == nil && exit == 0
}
