package distrokind

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveDistro_FieldCopyAndMethods covers the distro de-type (Cutover M):
// OpResolve projects spec.Distro → ResolvedDistro (field-copy + Raw), and the mirrored
// PrimaryFormat method must skip a secondary format. Without the resolve leg + the
// method mirror, the build engine cannot read the de-typed distro.
func TestResolveDistro_FieldCopyAndMethods(t *testing.T) {
	body, err := json.Marshal(spec.Distro{
		Version: "1",
		Format: map[string]*spec.Format{
			"deb": {Secondary: true},
			"rpm": {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := resolveDistro(spec.DistroResolveInput{Distro: body})
	if err != nil {
		t.Fatalf("resolveDistro: %v", err)
	}
	r := reply.Resolved
	if r == nil || r.Version != "1" || len(r.Format) != 2 {
		t.Fatalf("field copy failed: %+v", r)
	}
	if got := r.PrimaryFormat(); got != "rpm" {
		t.Errorf("PrimaryFormat = %q, want rpm (deb is secondary)", got)
	}
	if string(r.Raw) != string(body) {
		t.Errorf("Raw not preserved through resolve")
	}
}
