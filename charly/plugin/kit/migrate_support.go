package kit

// migrate_support.go — the stable schema/ledger path constants shared between
// charly core and the importable kit. The migration chain, its host-prelifted
// runtime context / reply types, and the plural→singular key map folded back into
// charly core at the migration-baseline reset (the former out-of-core migration
// module and its cross-boundary contract were folded away); only these constants — read on
// the current-format load + ledger paths, not migration — remain here.

// Project path constants shared by core and kit. Core aliases each via `const X = kit.X`.
const (
	UnifiedFileName = "charly.yml" // the ONE box/candy manifest filename
	DefaultBoxDir   = "box"        // discovered box/<name>/ directory
	DefaultCandyDir = "candy"      // discovered candy/<name>/ directory
)

// LedgerSchemaVersion is the install-ledger record format version, DECOUPLED from
// the project schema HEAD so a non-ledger cutover never invalidates a migrated
// ledger. Read by core's ledger path (ReadDeployRecord/ReadCandyRecord, which
// hard-reject a record lacking this stamp). Core aliases via `const … = kit.LedgerSchemaVersion`.
const LedgerSchemaVersion = "2026.161.1649"
