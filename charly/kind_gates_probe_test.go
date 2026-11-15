package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestLoaderThreadedCarriesTheAuthoredAndWireShapes pins the gates the host threads, measured on
// the tree it ships: 20 gates, and the `vm` gate carries the AUTHORED body in Def with the wire
// value beside it in WireDef — the split spec#328 declares. It fails if the fill stops threading,
// if the registry key shape regresses (`class:word`, not `class/word` — an empty map is what the
// wrong separator produced), or if the two shapes collapse back into one.
func TestLoaderThreadedCarriesTheAuthoredAndWireShapes(t *testing.T) {
	tg := loaderThreaded()
	if len(tg.KindGates) == 0 {
		t.Fatal("no kind gates were threaded — the fill produced an empty map")
	}
	g, ok := tg.KindGates["vm"]
	if !ok {
		t.Fatalf("the vm gate is absent from %d threaded gates", len(tg.KindGates))
	}
	if g.Def != "#Vm" {
		t.Fatalf("Def = %q, want the AUTHORED body #Vm", g.Def)
	}
	if g.WireDef != "#VmValue" {
		t.Fatalf("WireDef = %q, want the wire value #VmValue", g.WireDef)
	}
}

// TestKindGatesDriveTheDefaultsFill is the R10 gate on the SEAM, not on the map: it proves the
// fill RESOLVES THROUGH the threaded gate. With the gate the probe kind's served def is found;
// with an EMPTY threaded value the same call cannot resolve a kind the base does not know, which
// is the assertion that fails if kindGates() stops serving the served schema.
func TestKindGatesDriveTheDefaultsFill(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if err := registerPluginUnitSchema("defprobe", PluginSchema{
		CueSource: "\n#DefprobeValue: {}\n#Defprobe: {\n\tx: string\n}\n",
		InputDefs: map[string]string{provKey(ClassKind, "defprobe"): "#DefprobeValue"},
	}); err != nil {
		t.Fatalf("register the defprobe unit: %v", err)
	}
	RegisterBuiltinProvider(&structProbeProv{capMeta: capMeta{class: ClassKind, word: "defprobe", structural: false, inputDef: "#DefprobeValue"}})

	g, ok := loaderThreaded().KindGates["defprobe"]
	if !ok {
		t.Fatalf("no gate threaded for defprobe in %d gate(s)", len(loaderThreaded().KindGates))
	}
	if g.Def != "#Defprobe" {
		t.Fatalf("Def = %q, want the authored def #Defprobe", g.Def)
	}
	if g.WireDef != "#DefprobeValue" {
		t.Fatalf("WireDef = %q, want the wire twin #DefprobeValue", g.WireDef)
	}
	body := struct {
		X string `json:"x"`
	}{X: "hi"}
	if err := requireProjectLoader().ApplyCueDefaults("defprobe", &body, loaderThreaded()); err != nil {
		t.Fatalf("the fill did not resolve through the threaded gate: %v", err)
	}
	// The NEGATIVE arm: the same call with no gates cannot resolve a plugin-owned kind.
	bare := struct {
		X string `json:"x"`
	}{X: "hi"}
	if err := requireProjectLoader().ApplyCueDefaults("defprobe", &bare, spec.Threaded{}); err == nil {
		t.Fatal("the fill resolved a plugin-owned kind WITHOUT the threaded gate — the test cannot fail")
	}
}

// TestKindGatesFillTheRealVmKind is the R10/R7 gate on the REAL recognized kind: `vm` is served
// by plugin-vm, so its authored def lives in the SERVED schema and is reachable only through
// spec.Threaded.KindGates. The fill must therefore produce the served schema's own defaults for a
// zero body, and must FAIL with an empty threaded value — the negative arm is what makes this test
// impossible to pass if the gate stops serving the served schema.
func TestKindGatesFillTheRealVmKind(t *testing.T) {
	v := spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", URL: "http://x"}}
	if err := requireProjectLoader().ApplyCueDefaults("vm", &v, loaderThreaded()); err != nil {
		t.Fatalf("filling a real vm body through the threaded gates failed: %v", err)
	}
	if v.Firmware != "bios" {
		t.Fatalf("firmware = %q, want the served default %q", v.Firmware, "bios")
	}
	if v.Backend != "auto" {
		t.Fatalf("backend = %q, want the served default %q", v.Backend, "auto")
	}
	if v.Autostart {
		t.Fatalf("autostart = true, want the served default false")
	}
	// The NEGATIVE arm: with no gates the vm def is unreachable from the base schema.
	bare := spec.ResolvedVm{Source: spec.VmSource{Kind: "cloud_image", URL: "http://x"}}
	if err := requireProjectLoader().ApplyCueDefaults("vm", &bare, spec.Threaded{}); err == nil {
		t.Fatal("the vm def resolved WITHOUT the threaded gate — the test cannot fail")
	}
}
