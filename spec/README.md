# opencharly/spec

The OpenCharly **wire/IR contract module**: `spec` (config / wire / InstallPlan-IR types, generated
from the CUE schema) + `proto` (the gRPC plugin transport). The dedicated contract that `charly`
core **and every plugin** import — extracted from `opencharly/sdk` as part of the #55 import-purity
program so core depends only on the contract, never on a mechanism kit.

- **Generate:** `task cue:gen` (spec types from `schema/*.cue`) + `task wire:gen` (proto from
  `protocol/schema/*.cue`). A clean regen is a no-op (reproducibility-gated).
- **Go-module tags:** `v0.<YYYYDDD>.<HHMM>` (semver-legal CalVer, leading zeros stripped).