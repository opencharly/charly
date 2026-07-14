package vm

import (
	"strconv"
	"testing"
)

// TestResolvePortForwards covers the pure allocation core of the extra-port-forward
// auto-allocation feature: fixed passthrough, auto-allocation (host ≠ guest,
// occupied-respected), prior-persisted reuse (idempotency across create→deploy-add),
// and the occupied-set collision guard.
func TestResolvePortForwards(t *testing.T) {
	t.Run("fixed passthrough is deterministic and allocates nothing", func(t *testing.T) {
		resolved, allocated, err := resolvePortForwards([]string{"2222:22", "16443:6443"}, nil, map[int]bool{}, "vm")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(allocated) != 0 {
			t.Errorf("fixed entries must allocate nothing; got %v", allocated)
		}
		if len(resolved) != 2 || resolved[0] != "2222:22" || resolved[1] != "16443:6443" {
			t.Errorf("fixed passthrough changed the strings; got %v", resolved)
		}
	})

	t.Run("auto allocates a free host port distinct from the guest", func(t *testing.T) {
		occupied := map[int]bool{}
		resolved, allocated, err := resolvePortForwards([]string{"auto:6443"}, nil, occupied, "vm")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		h, ok := allocated["6443"]
		if !ok || h <= 0 {
			t.Fatalf("auto:6443 must allocate a host port for guest 6443; got %v", allocated)
		}
		if h == 6443 {
			t.Errorf("allocated host port must differ from the guest port; got %d", h)
		}
		if !occupied[h] {
			t.Errorf("allocated host port %d must be recorded in the occupied set", h)
		}
		want := strconv.Itoa(h) + ":6443"
		if len(resolved) != 1 || resolved[0] != want {
			t.Errorf("resolved = %v; want [%q]", resolved, want)
		}
	})

	t.Run("auto reuses the prior persisted host port (idempotency)", func(t *testing.T) {
		resolved, allocated, err := resolvePortForwards([]string{"auto:6443"}, map[string]int{"6443": 45000}, map[int]bool{}, "vm")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if allocated["6443"] != 45000 || len(resolved) != 1 || resolved[0] != "45000:6443" {
			t.Errorf("prior 45000 not reused; resolved=%v allocated=%v", resolved, allocated)
		}
	})

	t.Run("a prior host already occupied is NOT reused — allocate fresh", func(t *testing.T) {
		// The prior 45000 is taken by a sibling (in the shared occupied set), so the
		// reuse guard falls through to a fresh allocation ≠ 45000.
		resolved, allocated, err := resolvePortForwards([]string{"auto:6443"}, map[string]int{"6443": 45000}, map[int]bool{45000: true}, "vm")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if allocated["6443"] == 45000 {
			t.Errorf("must not reuse an occupied prior host port 45000; got %v", allocated)
		}
		if allocated["6443"] <= 0 || resolved[0] != strconv.Itoa(allocated["6443"])+":6443" {
			t.Errorf("expected a fresh allocation; resolved=%v allocated=%v", resolved, allocated)
		}
	})

	t.Run("mixed auto + fixed: auto never collides with a seeded occupied port", func(t *testing.T) {
		// Seed occupied with a fake ssh host port; the auto forward must avoid it.
		occupied := map[int]bool{54321: true}
		resolved, allocated, err := resolvePortForwards([]string{"auto:6443", "2222:22"}, nil, occupied, "vm")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if allocated["6443"] == 54321 {
			t.Errorf("auto forward collided with the seeded occupied port 54321")
		}
		if len(resolved) != 2 || resolved[1] != "2222:22" {
			t.Errorf("fixed entry lost in mixed list; got %v", resolved)
		}
	})

	t.Run("invalid guest port errors", func(t *testing.T) {
		if _, _, err := resolvePortForwards([]string{"auto:0"}, nil, map[int]bool{}, "vm"); err == nil {
			t.Errorf("auto:0 (guest 0) must error")
		}
	})
}

// TestBuildDefaultInterface_ForwardsFromResolvedRtOnly proves the libvirt renderer
// emits passt <portForward> ranges ONLY from the RESOLVED rt.ExtraPortForwards, never
// from spec.Network.PortForwards. FAILS on the pre-feature renderer (which looped
// net.PortForwards and would emit a Start=16443 range below).
func TestBuildDefaultInterface_ForwardsFromResolvedRtOnly(t *testing.T) {
	spec := &VmSpec{Network: &VmNetwork{Mode: "user", PortForwards: []string{"auto:6443", "16443:6443"}}}
	rt := VmRuntimeParams{SshPort: 2299, ExtraPortForwards: []string{"45000:6443"}}

	out := buildDefaultInterface(spec, rt)
	if len(out.PortForward) != 1 {
		t.Fatalf("expected one <portForward>; got %d", len(out.PortForward))
	}
	starts := map[uint]uint{} // Start(host) → To(guest)
	for _, r := range out.PortForward[0].Ranges {
		starts[r.Start] = r.To
	}
	if starts[2299] != 22 {
		t.Errorf("ssh forward range 2299→22 missing; got %v", starts)
	}
	if starts[45000] != 6443 {
		t.Errorf("resolved rt.ExtraPortForwards range 45000→6443 missing; got %v", starts)
	}
	if _, leaked := starts[16443]; leaked {
		t.Errorf("raw spec.Network.PortForwards host 16443 rendered — the direct-spec read was NOT deleted: %v", starts)
	}
}
