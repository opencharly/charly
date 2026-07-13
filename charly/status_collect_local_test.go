package main

import (
	"context"
	"sort"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// redirectLocalLedger points localLedgerPaths at a fresh temp ledger for the
// test's duration and returns the LedgerPaths. Reuses the canonical
// withTempLedger (host_infra_test.go) for the LedgerPaths construction (R3 —
// no duplicated path literal) and layers on the two things this collector's
// tests need that the base helper doesn't: redirecting the swappable
// localLedgerPaths var, and optionally creating the deploys/layers subdirs so
// Available() sees a real ledger. The "no ledger" case passes ensure=false.
func redirectLocalLedger(t *testing.T, ensure bool) *LedgerPaths {
	t.Helper()
	paths := withTempLedger(t)
	if ensure {
		if err := paths.Ensure(); err != nil {
			t.Fatalf("ledger ensure: %v", err)
		}
	}
	prev := localLedgerPaths
	localLedgerPaths = func() (*LedgerPaths, error) { return paths, nil }
	t.Cleanup(func() { localLedgerPaths = prev })
	return paths
}

func collectLocal(t *testing.T) []spec.DeploymentStatus {
	t.Helper()
	lc := &LocalCollector{}
	rows, err := lc.Collect(context.Background(), CollectOpts{RunMode: "quadlet"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rows
}

func TestLocalCollector_Kind(t *testing.T) {
	lc := &LocalCollector{}
	if lc.Kind() != spec.SubstrateLocal {
		t.Errorf("Kind() = %q, want %q", lc.Kind(), spec.SubstrateLocal)
	}
}

func TestLocalCollector_AvailableFalseWhenNoLedger(t *testing.T) {
	// ensure=false → deploys/ doesn't exist → substrate unavailable.
	redirectLocalLedger(t, false)
	lc := &LocalCollector{}
	if lc.Available(CollectOpts{}) {
		t.Error("Available() = true, want false when deploys/ is absent")
	}
}

func TestLocalCollector_AvailableTrueWhenLedgerExists(t *testing.T) {
	redirectLocalLedger(t, true)
	lc := &LocalCollector{}
	if !lc.Available(CollectOpts{}) {
		t.Error("Available() = false, want true when deploys/ exists")
	}
}

func TestLocalCollector_EmptyLedgerNoRows(t *testing.T) {
	redirectLocalLedger(t, true)
	rows := collectLocal(t)
	if len(rows) != 0 {
		t.Errorf("Collect() = %d rows, want 0 for an empty ledger", len(rows))
	}
}

// A plain host the local deploy target writes only CandyRecords (deploys/ stays
// empty). The collector must synthesize one row per deploy-id from the
// deployed_by sets — this is the real-world host case proven on live disk.
func TestLocalCollector_SynthesizesFromCandyRecords(t *testing.T) {
	paths := redirectLocalLedger(t, true)
	writeCandy(t, paths, &CandyRecord{
		Candy:      "ripgrep",
		DeployedBy: []string{"deploy-A"},
		DeployedAt: "2026-05-30T10:00:00Z",
	})
	writeCandy(t, paths, &CandyRecord{
		Candy:      "uv",
		DeployedBy: []string{"deploy-A", "deploy-B"},
		DeployedAt: "2026-05-31T12:00:00Z",
	})

	rows := collectLocal(t)
	if len(rows) != 2 {
		t.Fatalf("Collect() = %d rows, want 2 (deploy-A, deploy-B)", len(rows))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Container < rows[j].Container })

	a := rows[0]
	if a.Container != "deploy-A" {
		t.Errorf("row[0].Container = %q, want deploy-A", a.Container)
	}
	if a.Kind != spec.SubstrateLocal || a.Source != "ledger" || a.Status != "applied" {
		t.Errorf("row[0] kind/source/status = %q/%q/%q, want local/ledger/applied", a.Kind, a.Source, a.Status)
	}
	if a.RunMode != "quadlet" {
		t.Errorf("row[0].RunMode = %q, want quadlet (from opts)", a.RunMode)
	}
	// deploy-A pulled both ripgrep + uv → 2 candies.
	if a.Image != "local (2 candies)" {
		t.Errorf("row[0].Image = %q, want local (2 candies)", a.Image)
	}
	// Newest deployed_at across its candies wins.
	if a.Uptime != "deployed 2026-05-31 12:00 UTC" {
		t.Errorf("row[0].Uptime = %q, want deployed 2026-05-31 12:00 UTC", a.Uptime)
	}

	b := rows[1]
	if b.Container != "deploy-B" {
		t.Errorf("row[1].Container = %q, want deploy-B", b.Container)
	}
	// deploy-B pulled only uv → 1 candy (singular label).
	if b.Image != "local (1 candy)" {
		t.Errorf("row[1].Image = %q, want local (1 candy)", b.Image)
	}
}

// An explicit DeployRecord (VM-target local deploy, or a future host write) is
// honored; its candy set + add_candy merge, and a CandyRecord referencing the
// same deploy-id must NOT create a second row.
func TestLocalCollector_DeployRecordUnionNoDoubleCount(t *testing.T) {
	paths := redirectLocalLedger(t, true)
	if err := WriteDeployRecord(paths, &DeployRecord{
		DeployID:   "deploy-X",
		Target:     "vm:check-arch-vm",
		Candy:      []string{"base", "charly"},
		AddCandy:   []string{"sshkeys"},
		DeployedAt: "2026-05-29T08:00:00Z",
	}); err != nil {
		t.Fatalf("WriteDeployRecord: %v", err)
	}
	// A CandyRecord for the SAME deploy-id, plus one extra candy not in the
	// deploy record's lists.
	writeCandy(t, paths, &CandyRecord{
		Candy:      "extra-layer",
		DeployedBy: []string{"deploy-X"},
		DeployedAt: "2026-05-29T09:00:00Z",
	})

	rows := collectLocal(t)
	if len(rows) != 1 {
		t.Fatalf("Collect() = %d rows, want 1 (no double-count for deploy-X)", len(rows))
	}
	r := rows[0]
	if r.Container != "deploy-X" {
		t.Errorf("Container = %q, want deploy-X", r.Container)
	}
	// base + charly + sshkeys (record) + extra-layer (candy pass) = 4 distinct.
	if r.Image != "local (4 candies)" {
		t.Errorf("Image = %q, want local (4 candies)", r.Image)
	}
	// Newest across record (08:00) and candy (09:00) wins.
	if r.Uptime != "deployed 2026-05-29 09:00 UTC" {
		t.Errorf("Uptime = %q, want deployed 2026-05-29 09:00 UTC", r.Uptime)
	}
}

func TestNewerTimestamp(t *testing.T) {
	cases := []struct {
		name, a, b, want string
	}{
		{"a empty", "", "2026-05-31T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"b empty", "2026-05-31T00:00:00Z", "", "2026-05-31T00:00:00Z"},
		{"b newer", "2026-05-30T00:00:00Z", "2026-05-31T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"a newer", "2026-05-31T00:00:00Z", "2026-05-30T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"a malformed", "garbage", "2026-05-30T00:00:00Z", "2026-05-30T00:00:00Z"},
		{"b malformed", "2026-05-30T00:00:00Z", "garbage", "2026-05-30T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := newerTimestamp(c.a, c.b); got != c.want {
				t.Errorf("newerTimestamp(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestFormatLedgerTimestamp(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"not-a-time", ""},
		{"2026-05-31T12:34:56Z", "deployed 2026-05-31 12:34 UTC"},
	}
	for _, c := range cases {
		if got := formatLedgerTimestamp(c.in); got != c.want {
			t.Errorf("formatLedgerTimestamp(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLocalDeployLabel(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "local (0 candies)"},
		{1, "local (1 candy)"},
		{3, "local (3 candies)"},
	}
	for _, c := range cases {
		if got := localDeployLabel(c.n); got != c.want {
			t.Errorf("localDeployLabel(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// writeCandy serializes a CandyRecord into the temp ledger's layers/ dir.
func writeCandy(t *testing.T, paths *LedgerPaths, rec *CandyRecord) {
	t.Helper()
	if err := WriteCandyRecord(paths, rec); err != nil {
		t.Fatalf("WriteCandyRecord(%s): %v", rec.Candy, err)
	}
}
