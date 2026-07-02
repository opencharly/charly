package main

// status_collect_local.go — the LOCAL substrate SubstrateCollector.
//
// `target: local` deployments (host filesystem applies via ShellExecutor,
// and SSH applies via SSHExecutor) record themselves in the install ledger
// at ~/.config/opencharly/installed/ (see install_ledger.go). Unlike the pod
// substrate — whose live truth is `podman ps` — the local substrate has no
// running container to inspect; the ledger IS the authoritative state of
// "what has been applied to this filesystem".
//
// Ledger shape (verified against live on-disk state):
//
//   deploys/<deploy-id>.json   DeployRecord — written by the external vm deploy for
//                              its guest-side ledger; the host the local deploy target
//                              records at CANDY granularity only, so this dir is
//                              typically EMPTY for plain host deploys.
//   candy/<candy>.json        CandyRecord — written by EVERY local apply
//                              (AddCandyDeploymentVia). `deployed_by` is the set
//                              of deploy-ids that pulled this candy in; this is
//                              the populated, authoritative source.
//
// Because the host the local deploy target never writes a DeployRecord, a collector
// that read deploys/ alone would emit ZERO rows for real host deploys. So this
// collector takes the UNION: every explicit DeployRecord in deploys/, PLUS a
// synthesized row per deploy-id that appears in some CandyRecord.deployed_by
// but has no DeployRecord. No deploy-id is double-counted. One
// DeploymentStatus{Kind: local, Source: "ledger"} per deploy-id, with the
// applied-candy count and the most-recent deployed_at surfaced.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// localLedgerPaths is the swappable ledger-paths resolver, defaulting to the
// canonical DefaultLedgerPaths. Tests redirect it at a temp dir — mirrors the
// InspectContainer / InspectLabels swappable-var pattern used elsewhere in the
// package for filesystem/engine boundaries.
var localLedgerPaths = DefaultLedgerPaths

// LocalCollector is the local-substrate SubstrateCollector. It reads the
// install ledger (never the engine) and reconstructs one row per deploy-id.
type LocalCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &LocalCollector{c: c} })
}

// Kind reports the local substrate.
func (l *LocalCollector) Kind() SubstrateKind { return SubstrateLocal }

// Available reports whether the ledger's deploys/ dir exists. Absence means no
// local deploy has ever run on this host, so the substrate contributes no rows
// and is skipped silently (no error). A resolver error (e.g. no HOME) also
// gates the substrate off rather than erroring the whole command.
func (l *LocalCollector) Available(opts CollectOpts) bool {
	paths, err := localLedgerPaths()
	if err != nil {
		return false
	}
	info, err := os.Stat(paths.Deploys)
	return err == nil && info.IsDir()
}

// Collect builds one DeploymentStatus per deploy-id found in the ledger.
//
// Two passes over the ledger, unioned by deploy-id:
//  1. deploys/<id>.json DeployRecords (explicit; written by VM-target local
//     deploys and any future host DeployRecord write).
//  2. candy/<candy>.json CandyRecords — every deploy-id in a candy's
//     deployed_by set that wasn't already covered by a DeployRecord gets a
//     synthesized row from the candies that reference it.
//
// For each deploy-id we surface: the applied-candy count (Image cell, since a
// kind:local deploy has no container image), the deploy-id (Container cell),
// and the most-recent deployed_at as an absolute UTC timestamp (Uptime cell).
func (l *LocalCollector) Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error) {
	paths, err := localLedgerPaths()
	if err != nil {
		return nil, fmt.Errorf("local ledger paths: %w", err)
	}

	// deployAgg accumulates per-deploy-id facts gathered across both ledger
	// passes. candySet dedupes candy names; latest tracks the newest
	// deployed_at across the deploy record and every contributing layer.
	type deployAgg struct {
		candySet   map[string]bool
		latest     string // RFC3339, newest deployed_at seen
		fromRecord bool   // had an explicit DeployRecord
		target     string // DeployRecord.Target ("" for synthesized)
	}
	aggs := map[string]*deployAgg{}
	get := func(id string) *deployAgg {
		a := aggs[id]
		if a == nil {
			a = &deployAgg{candySet: map[string]bool{}}
			aggs[id] = a
		}
		return a
	}

	// Pass 1: explicit DeployRecords in deploys/.
	deployIDs, err := ledgerJSONStems(paths.Deploys)
	if err != nil {
		return nil, fmt.Errorf("local ledger deploys: %w", err)
	}
	for _, id := range deployIDs {
		rec, err := ReadDeployRecord(paths, id)
		if err != nil {
			// One unreadable record degrades to a stderr note, not a whole-
			// substrate failure — matches Collector.All's per-collector
			// graceful-degradation contract.
			fmt.Fprintf(os.Stderr, "WARNING: charly status: local collector: %v\n", err)
			continue
		}
		if rec == nil {
			continue
		}
		a := get(rec.DeployID)
		a.fromRecord = true
		a.target = rec.Target
		for _, ln := range rec.Candy {
			a.candySet[ln] = true
		}
		for _, ln := range rec.AddCandy {
			a.candySet[ln] = true
		}
		a.latest = newerTimestamp(a.latest, rec.DeployedAt)
	}

	// Pass 2: CandyRecords in candy/ — attribute each candy to every deploy-id
	// in its deployed_by set (synthesizing rows for deploy-ids with no
	// DeployRecord, enriching the candy set of those that have one).
	candyNames, err := ledgerJSONStems(paths.Candies)
	if err != nil {
		return nil, fmt.Errorf("local ledger candies: %w", err)
	}
	for _, ln := range candyNames {
		rec, err := ReadCandyRecord(paths, ln)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: charly status: local collector: %v\n", err)
			continue
		}
		if rec == nil {
			continue
		}
		for _, id := range rec.DeployedBy {
			a := get(id)
			a.candySet[rec.Candy] = true
			a.latest = newerTimestamp(a.latest, rec.DeployedAt)
		}
	}

	rows := make([]DeploymentStatus, 0, len(aggs))
	for id, a := range aggs {
		rows = append(rows, DeploymentStatus{
			Kind:      SubstrateLocal,
			Source:    "ledger",
			Image:     localDeployLabel(len(a.candySet)),
			Status:    "applied",
			Uptime:    formatLedgerTimestamp(a.latest),
			Container: id,
			RunMode:   opts.RunMode,
		})
	}

	// Deterministic ordering by deploy-id; Collector.All re-sorts the merged
	// set across substrates, but a stable order here keeps test output and the
	// single-substrate case predictable.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Container < rows[j].Container })
	return rows, nil
}

// ledgerJSONStems returns the base names (without the .json suffix) of every
// *.json file directly under dir. A missing dir is not an error — it yields an
// empty slice, so a host with a deploys/ dir but no candy/ dir (or vice
// versa) collects cleanly.
func ledgerJSONStems(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var stems []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		stems = append(stems, strings.TrimSuffix(name, ".json"))
	}
	return stems, nil
}

// localDeployLabel renders the IMAGE-cell text for a local deploy, which has no
// container image. It reports the applied-candy count instead.
func localDeployLabel(n int) string {
	if n == 1 {
		return "local (1 candy)"
	}
	return fmt.Sprintf("local (%d candies)", n)
}

// newerTimestamp returns whichever of two RFC3339 timestamps is later. A
// non-empty value beats empty; an unparseable value is treated as older than a
// parseable one so a malformed record never masks a good deployed_at.
func newerTimestamp(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	switch {
	case errA != nil && errB != nil:
		return a
	case errA != nil:
		return b
	case errB != nil:
		return a
	}
	if tb.After(ta) {
		return b
	}
	return a
}

// formatLedgerTimestamp renders a ledger deployed_at (RFC3339) for the Uptime
// cell. Ledger deploys have no "uptime" — the filesystem state persists — so we
// surface the apply time as an absolute UTC instant ("deployed YYYY-MM-DD
// HH:MM UTC"). An empty or unparseable value yields "".
func formatLedgerTimestamp(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return "deployed " + t.UTC().Format("2006-01-02 15:04 MST")
}
