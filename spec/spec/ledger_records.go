package spec

// ledger_records.go — SPIKE: the install-ledger record shapes relocated from
// sdk/kit/install_ledger.go + sdk/kit/migrate_support.go (#55 value-type
// relocation spike, cluster 4). Every field already resolved to a spec.* type
// (StepKind/Scope/Venue/ReverseOp are all spec aliases in sdk/kit) and none of
// the three record types carry any methods, so they moved verbatim. kit.*
// becomes a type/const alias onto these.

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
	Candy         []string `json:"candy,omitempty"`
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

// LedgerSchemaVersion is the install-ledger record format version (the
// ledger-candy-keys cutover's CalVer). It is INDEPENDENT of the project schema
// CalVer (LatestSchemaVersion) — a non-ledger schema cutover that bumps the
// project HEAD must NOT invalidate the ledger gate.
const LedgerSchemaVersion = "2026.161.1649"
