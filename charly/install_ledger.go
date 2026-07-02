package main

// install_ledger.go — persistent record of host deploys.
//
// Every `charly bundle add host …` writes structured records to a ledger
// so a later `charly bundle del host …` can reverse the exact operations.
// Layout:
//
//   ~/.config/opencharly/installed/
//     .lock                          flock for concurrent sessions
//     deploys/
//       <deploy-id>.json             image + add_candy + candy list
//     candy/
//       <candy-name>.json            per-candy steps + deployed_by set
//
// Refcounting lives in the candy files: `deployed_by` is the set of
// deploy IDs that include this candy. Uninstalling one deploy
// decrements the set; only when it becomes empty does the candy's
// steps actually reverse.
//
// This file implements I/O (read/write/lock) and ledger-shape types.
// The actual reverse-execution logic lives in deploy_host_helpers.go.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"context"

	"github.com/opencharly/charly/charly/plugin/kit"
)

// LedgerPaths describes where ledger files live on disk. Extracted so
// tests can redirect to a temp dir.
type LedgerPaths struct {
	Root     string // ~/.config/opencharly/installed
	Deploys  string // <Root>/deploys/
	Candies  string // <Root>/layers/
	LockFile string // <Root>/.lock
}

// DefaultLedgerPaths returns the canonical paths anchored at the
// invoking user's home directory.
func DefaultLedgerPaths() (*LedgerPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("DefaultLedgerPaths: %w", err)
	}
	root := filepath.Join(home, ".config", "opencharly", "installed")
	return &LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}, nil
}

// Ensure creates the ledger directory tree if missing.
func (p *LedgerPaths) Ensure() error {
	for _, d := range []string{p.Root, p.Deploys, p.Candies} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("ledger mkdir %s: %w", d, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Flock — serialize concurrent charly bundle sessions.
// ---------------------------------------------------------------------------

// LedgerLock is an acquired advisory lock on the ledger directory. Call
// Release() when done. Panic-safe via defer.
type LedgerLock struct {
	release func() error
}

// AcquireLedgerLock takes a blocking exclusive flock on the ledger lock file
// via the shared acquireFileLock primitive (filelock.go). Blocks until the
// lock is available.
func AcquireLedgerLock(paths *LedgerPaths) (*LedgerLock, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	release, err := acquireFileLock(paths.LockFile, true)
	if err != nil {
		return nil, fmt.Errorf("ledger lock: %w", err)
	}
	return &LedgerLock{release: release}, nil
}

// Release releases the flock and closes the file.
func (l *LedgerLock) Release() error {
	if l == nil || l.release == nil {
		return nil
	}
	err := l.release()
	l.release = nil
	return err
}

// ---------------------------------------------------------------------------
// Ledger records
// ---------------------------------------------------------------------------

// DeployRecord is the top-level entry in deploys/<deploy-id>.json.
// Lists the image, tag, and the ordered candy set included in this
// deploy (image candies + add_candy overlays, already topo-sorted).
type DeployRecord struct {
	// SchemaVersion is the ledger-format version (the ledger-candy-keys
	// cutover's CalVer). Empty means a pre-cutover record (json:"layer"
	// keys) — the read path rejects it with a `charly migrate` hint.
	SchemaVersion string   `json:"schema_version,omitempty"`
	DeployID      string   `json:"deploy_id"`
	Image         string   `json:"image"`
	Tag           string   `json:"tag,omitempty"`
	Target        string   `json:"target"` // the deploy-record key, e.g. "vm:<name>" (only VM/local deploys write a DeployRecord)
	Candy         []string `json:"candy"`
	AddCandy      []string `json:"add_candy,omitempty"`
	DeployedAt    string   `json:"deployed_at"`
}

// CandyRecord is the per-candy ledger entry. Lists concrete artifacts
// (packages installed, files written, services enabled, env.d file
// created, repo changes) so reversal doesn't need to re-compile the
// plan from the candy manifest.
type CandyRecord struct {
	SchemaVersion string       `json:"schema_version,omitempty"`
	Candy         string       `json:"candy"`
	Version       string       `json:"version,omitempty"`
	DeployedBy    []string     `json:"deployed_by"` // set of deploy IDs
	DeployedAt    string       `json:"deployed_at"`
	BuilderImage  string       `json:"builder_image,omitempty"`
	Steps         []StepRecord `json:"steps,omitempty"`       // completed steps, in install order
	ReverseOps    []ReverseOp  `json:"reverse_ops,omitempty"` // precomputed ops for teardown
}

// StepRecord is a thin summary of a completed InstallStep that the
// ledger keeps for audit. Kept intentionally small — the ReverseOps
// list on CandyRecord is the source of truth for teardown.
type StepRecord struct {
	Kind        StepKind          `json:"kind"`
	Scope       Scope             `json:"scope,omitempty"`
	Venue       Venue             `json:"venue,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	CompletedAt string            `json:"completed_at"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// ---------------------------------------------------------------------------
// I/O
// ---------------------------------------------------------------------------

// WriteDeployRecord serializes rec to deploys/<deploy-id>.json.
// ledgerSchemaVersion is the install-ledger record format version (the
// ledger-candy-keys cutover's CalVer). It is INDEPENDENT of the project schema
// CalVer (LatestSchemaVersion) — a non-ledger schema cutover that bumps the
// project HEAD must NOT invalidate the ledger gate. Every record written
// carries it; the read path rejects a record without it (a pre-cutover record
// whose json:"layer" key would silently unmarshal to an empty Candy).
// The value lives in kit (the importable host-engine shared with out-of-tree
// plugin candies); this is the in-core alias.
const ledgerSchemaVersion = kit.LedgerSchemaVersion

func WriteDeployRecord(paths *LedgerPaths, rec *DeployRecord) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	rec.SchemaVersion = ledgerSchemaVersion
	path := filepath.Join(paths.Deploys, rec.DeployID+".json")
	if err := ValidateEgressValue("deploy_record", path, rec); err != nil {
		return err
	}
	return writeJSONAtomic(path, rec)
}

// ReadDeployRecord loads deploys/<deploy-id>.json; returns nil, nil if
// the file doesn't exist.
func ReadDeployRecord(paths *LedgerPaths, id string) (*DeployRecord, error) {
	path := filepath.Join(paths.Deploys, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ReadDeployRecord: %w", err)
	}
	var rec DeployRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("ReadDeployRecord: parsing %s: %w", path, err)
	}
	if rec.SchemaVersion == "" {
		return nil, fmt.Errorf("ReadDeployRecord: %s is a pre-cutover install-ledger record (legacy json:\"layer\" keys, no schema_version) — remove the stale record (it regenerates on the next deploy)", path)
	}
	return &rec, nil
}

// WriteCandyRecord serializes rec to candy/<candy>.json.
func WriteCandyRecord(paths *LedgerPaths, rec *CandyRecord) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	rec.SchemaVersion = ledgerSchemaVersion
	path := filepath.Join(paths.Candies, rec.Candy+".json")
	if err := ValidateEgressValue("candy_record", path, rec); err != nil {
		return err
	}
	return writeJSONAtomic(path, rec)
}

// ReadCandyRecord loads candy/<candy>.json; returns nil, nil if absent.
func ReadCandyRecord(paths *LedgerPaths, layer string) (*CandyRecord, error) {
	path := filepath.Join(paths.Candies, layer+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ReadCandyRecord: %w", err)
	}
	var rec CandyRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("ReadCandyRecord: parsing %s: %w", path, err)
	}
	if rec.SchemaVersion == "" {
		return nil, fmt.Errorf("ReadCandyRecord: %s is a pre-cutover install-ledger record (legacy json:\"layer\" keys, no schema_version) — remove the stale record (it regenerates on the next deploy)", path)
	}
	return &rec, nil
}

// DeleteDeployRecord removes deploys/<deploy-id>.json; silently ignores
// not-found (teardown is idempotent).
func DeleteDeployRecord(paths *LedgerPaths, id string) error {
	path := filepath.Join(paths.Deploys, id+".json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeleteCandyRecord removes candy/<candy>.json.
func DeleteCandyRecord(paths *LedgerPaths, layer string) error {
	path := filepath.Join(paths.Candies, layer+".json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeJSONAtomic writes data to path via a temp file + rename so
// readers never see a partial write.
func writeJSONAtomic(path string, data any) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Refcount helpers
// ---------------------------------------------------------------------------

// AddCandyDeployment adds deployID to candy.DeployedBy and writes the
// record. Used at install time.
func AddCandyDeployment(paths *LedgerPaths, candyName, deployID string, update func(*CandyRecord)) error {
	rec, err := ReadCandyRecord(paths, candyName)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &CandyRecord{
			Candy:      candyName,
			DeployedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if !containsString(rec.DeployedBy, deployID) {
		rec.DeployedBy = append(rec.DeployedBy, deployID)
	}
	if update != nil {
		update(rec)
	}
	return WriteCandyRecord(paths, rec)
}

// RemoveCandyDeployment decrements a candy's deployed_by set. Returns
// (recordAfter, shouldFullyRemove, error). When shouldFullyRemove is
// true, the caller should perform the actual file/package/service
// teardown and then delete the candy ledger entry.
func RemoveCandyDeployment(paths *LedgerPaths, candyName, deployID string) (*CandyRecord, bool, error) {
	rec, err := ReadCandyRecord(paths, candyName)
	if err != nil {
		return nil, false, err
	}
	if rec == nil {
		return nil, false, nil // already gone
	}
	out := rec.DeployedBy[:0]
	for _, id := range rec.DeployedBy {
		if id != deployID {
			out = append(out, id)
		}
	}
	rec.DeployedBy = out
	if len(rec.DeployedBy) == 0 {
		return rec, true, nil
	}
	return rec, false, WriteCandyRecord(paths, rec)
}

func containsString(s []string, v string) bool {
	return slices.Contains(s, v)
}

// ---------------------------------------------------------------------------
// Executor-routed ledger I/O for nested deploys.
//
// A nested host-deploy (e.g. arch-vm.arch-host — host-target running
// INSIDE a VM via SSH) must write its ledger on the SUBSTRATE
// filesystem (guest HOME), not on the operator's local FS. The
// ancestor-executor-chain derivation in deploy_add_cmd.go already
// routes install commands through the correct executor; the ledger
// needs the same treatment.
// ---------------------------------------------------------------------------

// AddCandyDeploymentVia is the executor-routed variant of
// AddCandyDeployment. When exec is nil or a local executor, it
// falls back to operator-side file I/O (today's behaviour). When exec
// is a non-local DeployExecutor (SSHExecutor / NestedExecutor), the
// ledger file I/O goes through exec.GetFile + exec.RunSystem so the
// ledger lands on the substrate's filesystem under the substrate's
// ~/.config/opencharly/installed/ — matching the install's actual
// venue (arch-vm.arch-host writes in the arch VM guest; sway-pod with
// nested pods writes in the parent pod; etc.).
func AddCandyDeploymentVia(exec DeployExecutor, paths *LedgerPaths, candyName, deployID string, update func(*CandyRecord)) error {
	if exec == nil {
		return AddCandyDeployment(paths, candyName, deployID, update)
	}
	if _, isLocal := exec.(ShellExecutor); isLocal {
		return AddCandyDeployment(paths, candyName, deployID, update)
	}
	ctx := context.Background()
	// Substrate ledger dirs. The layers-dir basename stays `layers` (NOT `candy`)
	// — it must match DefaultLedgerPaths.Candies and the path the local reader
	// expects (the box/candy rebrand deliberately preserved this on-disk ledger
	// path). Single-source candiesDir/deploysDir so the write target and the mkdir
	// can NEVER diverge again (the rebrand's bug was exactly that divergence —
	// write went to installed/candy/ while mkdir created installed/layers/, so the
	// write failed on every fresh substrate). `~` resolves in the substrate shell.
	const installedRoot = "~/.config/opencharly/installed"
	candiesDir := installedRoot + "/layers"
	deploysDir := installedRoot + "/deploys"
	remoteFile := candiesDir + "/" + candyName + ".json"
	// Create BOTH installed/layers and installed/deploys so the full ledger
	// directory tree (matching Ensure()) exists on the substrate — ensures bed
	// tests like `test -d ~/.config/opencharly/installed/deploys` pass even when no
	// DeployRecord has been written yet.
	mkdirScript := "mkdir -p " + candiesDir + " " + deploysDir
	data, err := exec.GetFile(ctx, remoteFile, false, EmitOpts{})
	var rec *CandyRecord
	if err == nil && len(data) > 0 {
		rec = &CandyRecord{}
		if jerr := json.Unmarshal(data, rec); jerr != nil {
			rec = nil
		}
	}
	if rec == nil {
		rec = &CandyRecord{
			Candy:      candyName,
			DeployedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if !containsString(rec.DeployedBy, deployID) {
		rec.DeployedBy = append(rec.DeployedBy, deployID)
	}
	if update != nil {
		update(rec)
	}
	rec.SchemaVersion = ledgerSchemaVersion
	if err := ValidateEgressValue("candy_record", remoteFile, rec); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("AddCandyDeploymentVia: marshal: %w", err)
	}
	script := mkdirScript + " && cat > " + remoteFile + " <<'CHARLY_LEDGER_EOF'\n" +
		string(encoded) + "\nCHARLY_LEDGER_EOF\n"
	if runErr := exec.RunUser(ctx, script, EmitOpts{}); runErr != nil {
		return fmt.Errorf("AddCandyDeploymentVia: write via executor: %w", runErr)
	}
	return nil
}

// WriteDeployRecordVia is the executor-routed variant of
// WriteDeployRecord. Same semantics as AddCandyDeploymentVia but for
// deploy records (deploys/<id>.json).
func WriteDeployRecordVia(exec DeployExecutor, paths *LedgerPaths, rec *DeployRecord) error {
	if exec == nil {
		return WriteDeployRecord(paths, rec)
	}
	if _, isLocal := exec.(ShellExecutor); isLocal {
		return WriteDeployRecord(paths, rec)
	}
	ctx := context.Background()
	remoteFile := "~/.config/opencharly/installed/deploys/" + rec.DeployID + ".json"
	remoteDir := "~/.config/opencharly/installed/deploys"
	rec.SchemaVersion = ledgerSchemaVersion
	if err := ValidateEgressValue("deploy_record", remoteFile, rec); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("WriteDeployRecordVia: marshal: %w", err)
	}
	script := "mkdir -p " + remoteDir + " && cat > " + remoteFile + " <<'CHARLY_LEDGER_EOF'\n" +
		string(encoded) + "\nCHARLY_LEDGER_EOF\n"
	return exec.RunUser(ctx, script, EmitOpts{})
}
