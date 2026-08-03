package loader

// Shared test fixture: a spec.Threaded snapshot mirroring the REAL production
// substrate trait table (candy/plugin-substrate/plugin.go's substrateTraits) —
// the data ValidateCheckBeds/ValidateIterateBed consult host-side via the
// registry-derived snapshot in production. A plugin's own unit test builds
// this literal instead of querying a live provider registry.

import "github.com/opencharly/spec/spec"

func testThreaded() spec.Threaded {
	return spec.Threaded{
		DeployTraits: map[string]*spec.DeployTraits{
			"pod":     {Venue: "container", ImageBacked: true, ImageContext: true, BracketedLifecycle: true, BedTarget: true},
			"vm":      {Venue: "ssh", MachineVenue: true, ExclusiveVenue: true, BedTarget: true, SupportsEphemeral: true, SupportsFromSnapshot: true},
			"local":   {Venue: "shell", MachineVenue: true, BedTarget: true},
			"k8s":     {Venue: "shell", ImageContext: true, LeafOnly: true},
			"android": {Venue: "parent", BedTarget: true},
		},
	}
}
