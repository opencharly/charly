package main

// kit_ledger_aliases.go — package-main bindings onto the persistent host-deploy
// LEDGER (install_ledger.go), moved to sdk/kit in P4. The ledger records every
// `charly bundle add host …` so a later teardown can reverse the exact operations;
// charly's egress validation of each record is injected via kit.ValidateRecord.

import "github.com/opencharly/sdk/kit"

// ledgerSchemaVersion is the ledger record schema version stamped into every
// DeployRecord/CandyRecord — used by egress_test.go to assert the egress schema.
const ledgerSchemaVersion = kit.LedgerSchemaVersion

type (
	LedgerPaths  = kit.LedgerPaths
	DeployRecord = kit.DeployRecord
	CandyRecord  = kit.CandyRecord
)

var (
	DefaultLedgerPaths    = kit.DefaultLedgerPaths
	AcquireLedgerLock     = kit.AcquireLedgerLock
	WriteDeployRecord     = kit.WriteDeployRecord
	ReadDeployRecord      = kit.ReadDeployRecord
	DeleteDeployRecord    = kit.DeleteDeployRecord
	DeleteCandyRecord     = kit.DeleteCandyRecord
	AddCandyDeployment    = kit.AddCandyDeployment
	RemoveCandyDeployment = kit.RemoveCandyDeployment
	AddCandyDeploymentVia = kit.AddCandyDeploymentVia
	WriteDeployRecordVia  = kit.WriteDeployRecordVia
)

// Inject charly's egress-schema validation into the ledger's record-write path
// (sdk/kit has no egress subsystem — it calls the kit.ValidateRecord seam).
func init() {
	kit.ValidateRecord = ValidateEgressValue
}
