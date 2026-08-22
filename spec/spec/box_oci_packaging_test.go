package spec

// box_oci_packaging_test.go — the box OCI packaging surface (#Box entrypoint/cmd +
// #ResolvedBox entrypoint/cmd). A normal charly image bakes NO command into its OCI
// config; the deploy-time init (supervisord/systemd) is injected instead. The two
// opt-in fields exist for images spawned DIRECTLY from their baked OCI config with no
// charly deploy in the loop — e.g. the AgentTeams controller spawning manager/worker
// containers through a runtime socket. These tests prove the fields survive the YAML
// load (Box) and the wire serialization (ResolvedBox) the generator consumes.

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBox_OCIPackagingFields(t *testing.T) {
	// The manager-image declaration shape: an entrypoint (supervisord) with an
	// explicit empty cmd that clears the base image's inherited default command.
	body := `
entrypoint: [supervisord, -n, -c, /etc/supervisord.conf]
cmd: []
`
	var b Box
	if err := yaml.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	want := []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}
	if len(b.Entrypoint) != len(want) {
		t.Fatalf("Entrypoint = %v, want %v", b.Entrypoint, want)
	}
	for i := range want {
		if b.Entrypoint[i] != want[i] {
			t.Errorf("Entrypoint[%d] = %q, want %q", i, b.Entrypoint[i], want[i])
		}
	}
	// A DECLARED `cmd: []` must arrive as an EMPTY non-nil slice — the generator
	// distinguishes "declared empty" (clear the base Cmd) from "not declared"
	// (leave the base Cmd in place), so a nil slice here would silently resurrect
	// the base image's default command behind a baked entrypoint.
	if b.Cmd == nil {
		t.Fatal("Cmd = nil for `cmd: []`; want an empty declared slice (the emission-side clear signal)")
	}
	if len(b.Cmd) != 0 {
		t.Errorf("Cmd = %v, want empty", b.Cmd)
	}
}

func TestResolvedBox_OCIPackagingWire(t *testing.T) {
	// The resolved-box wire form carries the packaging fields so the plugin-build
	// resolve leg (which rebuilds the resolved box from the view) can emit them.
	rb := ResolvedBox{
		Name:       "app",
		Entrypoint: []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
		Cmd:        []string{"--foreground"},
	}
	data, err := json.Marshal(rb)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, key := range []string{"entrypoint", "cmd"} {
		if _, ok := got[key]; !ok {
			t.Errorf("ResolvedBox JSON missing %q field: %s", key, data)
		}
	}
	var back ResolvedBox
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip json.Unmarshal: %v", err)
	}
	if len(back.Entrypoint) != 4 || back.Entrypoint[3] != "/etc/supervisord.conf" {
		t.Errorf("round-trip Entrypoint = %v", back.Entrypoint)
	}
	if len(back.Cmd) != 1 || back.Cmd[0] != "--foreground" {
		t.Errorf("round-trip Cmd = %v", back.Cmd)
	}
}
