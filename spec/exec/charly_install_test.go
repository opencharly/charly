package exec

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

type bootstrapDeployExecutor struct {
	spec.DeployExecutor
	venueVersion  string
	replicated    bool
	replicatedTo  string
	replicatedBin []byte
}

func (e *bootstrapDeployExecutor) RunCapture(_ context.Context, script string) (string, string, int, error) {
	switch {
	case strings.Contains(script, "command -v charly"):
		return e.venueVersion, "", 0, nil
	case strings.Contains(script, "/tmp/charly-") && e.replicated:
		return "", "", 0, nil
	default:
		return "", "", 1, nil
	}
}

func (e *bootstrapDeployExecutor) PutFile(_ context.Context, localPath, remotePath string, _ uint32, ownerRoot bool, _ spec.EmitOpts) error {
	if ownerRoot {
		panic("endpoint bootstrap must remain user-scoped")
	}
	content, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	e.replicated = true
	e.replicatedTo = remotePath
	e.replicatedBin = content
	return nil
}

// TestHostCharlyIsNewer covers the single CalVer arbiter shared by EnsureCharlyInDeployVenue (walk-time,
// generic) + EnsureCharlyInGuest (vm PrepareVenue, host-surface) — moved here from charly core with
// the delivery logic (R3): the venue's PATH charly is kept when at least as new; only an absent/older
// one is overwritten, and an unprovable host version never clobbers.
func TestHostCharlyIsNewer(t *testing.T) {
	cases := []struct {
		name, host, venue string
		want              bool
	}{
		{"host strictly newer", "2026.200.1200", "2026.100.0900", true},
		{"venue newer — keep it", "2026.100.0900", "2026.200.1200", false},
		{"equal — never downgrade", "2026.150.1000", "2026.150.1000", false},
		{"venue absent — host wins", "2026.150.1000", "", true},
		{"venue unparseable — host wins", "2026.150.1000", "unknown", true},
		{"host unparseable — cannot prove, keep venue", "unknown", "2026.150.1000", false},
	}
	for _, c := range cases {
		if got := HostCharlyIsNewer(c.host, c.venue); got != c.want {
			t.Errorf("%s: HostCharlyIsNewer(%q,%q)=%v want %v", c.name, c.host, c.venue, got, c.want)
		}
	}
}

func TestEnsureCharlyInDeployVenueInstallationMatrix(t *testing.T) {
	controller := t.TempDir() + "/charly"
	if err := os.WriteFile(controller, []byte("controller-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("controller-binary"))
	replicatedPath := fmt.Sprintf("/tmp/charly-2026.200.1200-%x", digest[:8])
	for _, tc := range []struct {
		name         string
		venueVersion string
		wantPath     string
		wantCopy     bool
	}{
		{name: "absent", wantPath: replicatedPath, wantCopy: true},
		{name: "older", venueVersion: "2026.100.0900", wantPath: replicatedPath, wantCopy: true},
		{name: "equal", venueVersion: "2026.200.1200", wantPath: "charly"},
		{name: "newer", venueVersion: "2026.201.0001", wantPath: "charly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &bootstrapDeployExecutor{venueVersion: tc.venueVersion}
			got, err := EnsureCharlyInDeployVenue(context.Background(), exec, controller, "2026.200.1200")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantPath {
				t.Fatalf("endpoint = %q, want %q", got, tc.wantPath)
			}
			if exec.replicated != tc.wantCopy {
				t.Fatalf("replicated = %v, want %v", exec.replicated, tc.wantCopy)
			}
			if tc.wantCopy && (exec.replicatedTo != tc.wantPath || string(exec.replicatedBin) != "controller-binary") {
				t.Fatalf("replication = path %q bytes %q", exec.replicatedTo, exec.replicatedBin)
			}
		})
	}
}

func TestEnsureCharlyInDeployVenueReusesReplicatedBinary(t *testing.T) {
	controller := t.TempDir() + "/charly"
	if err := os.WriteFile(controller, []byte("controller-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	exec := &bootstrapDeployExecutor{replicated: true}
	got, err := EnsureCharlyInDeployVenue(context.Background(), exec, controller, "2026.200.1200")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("controller-binary"))
	want := fmt.Sprintf("/tmp/charly-2026.200.1200-%x", digest[:8])
	if got != want || exec.replicatedTo != "" {
		t.Fatalf("endpoint=%q new-copy=%q, want existing replicated endpoint", got, exec.replicatedTo)
	}
}
