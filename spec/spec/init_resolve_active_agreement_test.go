package spec

import "testing"

// fakeInitCandy implements only the CandyReader methods the init-resolution path
// touches (Capabilities, HasInit, RelayPorts). The embedded interface supplies the
// rest and is nil, so any other call panics loudly rather than returning a zero
// value that would quietly change what is under test.
type fakeInitCandy struct {
	CandyReader
	inits map[string]bool
	caps  *CandyCapability
}

func (f fakeInitCandy) HasInit(name string) bool       { return f.inits[name] }
func (f fakeInitCandy) RelayPorts() []int              { return nil }
func (f fakeInitCandy) Capabilities() *CandyCapability { return f.caps }

// TestResolveInitSystemAlwaysNamesAnActiveInit pins the invariant EmitInitAssembly
// depends on: a non-empty ResolveInitSystem result MUST be a key of ActiveInit.
//
// EmitInitAssembly enables a use_packaged: system unit through the resolved init
// only (`if initName == img.InitSystem`). If the resolved name is not an active
// init, that comparison matches nothing and every system unit in the image goes
// silently un-enabled — no build error, no warning, the service simply never
// starts. The invariant is what makes the narrowing safe, so it is asserted here
// rather than inferred from two functions happening to apply the same predicate.
func TestResolveInitSystemAlwaysNamesAnActiveInit(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		requires []string
		provides *CandyCapability
		wantInit string
	}{{
		// The regression: an explicit override whose capability is unmet. The
		// override branch used to return before the capability filter ran, so
		// this resolved to "systemd" while ActiveInit excluded it.
		name:     "explicit override with unmet capability",
		explicit: "systemd",
		requires: []string{"preserve_user"},
		provides: nil,
		wantInit: "",
	}, {
		name:     "explicit override with satisfied capability",
		explicit: "systemd",
		requires: []string{"preserve_user"},
		provides: &CandyCapability{PreserveUser: true},
		wantInit: "systemd",
	}, {
		name:     "explicit override, no capability required",
		explicit: "systemd",
		requires: nil,
		provides: nil,
		wantInit: "systemd",
	}, {
		name:     "auto-detect with unmet capability",
		explicit: "",
		requires: []string{"preserve_user"},
		provides: nil,
		wantInit: "",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ic := &InitConfig{Init: map[string]*ResolvedInit{
				"systemd": {RequiresCapability: tc.requires},
			}}
			layers := map[string]CandyReader{
				"c": fakeInitCandy{inits: map[string]bool{"systemd": true}, caps: tc.provides},
			}
			order := []string{"c"}

			got, def := ic.ResolveInitSystem(layers, order, tc.explicit)
			active := ic.ActiveInit(layers, order)

			if got != tc.wantInit {
				t.Errorf("ResolveInitSystem = %q, want %q", got, tc.wantInit)
			}
			// The invariant itself, independent of the expectation above.
			if got != "" {
				if _, ok := active[got]; !ok {
					t.Errorf("ResolveInitSystem returned %q, which is NOT a key of ActiveInit %v — "+
						"EmitInitAssembly would enable no system units at all", got, keysOf(active))
				}
				if def == nil {
					t.Errorf("ResolveInitSystem returned name %q with a nil def", got)
				}
			}
		})
	}
}

// TestResolveInitSystemHonorsAViableOverride is the discriminating half. The
// invariant test above pins that a resolved name is always active — it is
// satisfied by never honoring the override at all, so it stays green even if the
// override branch is deleted outright. This one fails in that case.
//
// The fixture triggers TWO inits and overrides to the one auto-detect would not
// pick: supervisord is preferred for container images, so without the override
// this composition resolves to supervisord. A single-triggered-init fixture
// cannot tell "override honored" from "auto-detect agreed".
func TestResolveInitSystemHonorsAViableOverride(t *testing.T) {
	want := &ResolvedInit{Model: "explicit-def"}
	ic := &InitConfig{Init: map[string]*ResolvedInit{
		"systemd":     want,
		"supervisord": {Model: "supervisord-def"},
	}}
	layers := map[string]CandyReader{
		"c": fakeInitCandy{inits: map[string]bool{"systemd": true, "supervisord": true}},
	}
	order := []string{"c"}

	// Control: the fixture only discriminates while auto-detect disagrees with
	// the override. If the preference rules change, this fails loudly rather than
	// letting the assertion below silently stop proving anything.
	if auto, _ := ic.ResolveInitSystem(layers, order, ""); auto != "supervisord" {
		t.Fatalf("control: ResolveInitSystem(no override) = %q, want supervisord — "+
			"fixture can no longer discriminate an honored override", auto)
	}

	name, def := ic.ResolveInitSystem(layers, order, "systemd")
	if name != "systemd" || def != want {
		t.Errorf("ResolveInitSystem(viable override) = (%q, %v), want (systemd, %v)", name, def, want)
	}
}

func keysOf(m map[string]*ResolvedInit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
