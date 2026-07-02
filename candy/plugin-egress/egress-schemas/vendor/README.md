# Vendored CUE schemas

These `.cue` files are upstream schemas converted to CUE **once, at dev time**, and
committed so charly stays a hermetic single binary — nothing is fetched from a CUE
registry or the network at build/runtime. They are embedded by
`//go:embed schema/vendor/*.cue` in `charly/egress.go` and compiled as their own
`cue.Value` (each carries a `package` clause + CUE-stdlib imports, so they cannot
join the concatenated `sharedCueSchema`). See `/charly-internals:egress`.

Regenerate with `task cue:vendor` (requires the `cue` CLI — the `/charly-tools:cue`
candy, pinned to the same `cuelang.org/go` version charly embeds). The generated
files are committed **pristine** so the reproducibility test can match the
`cue import` output.

## Sources & regen commands

| Vendored file | Def | Source (`sources/`) | Upstream | Command |
|---|---|---|---|---|
| `cloud_config.cue` | `#CloudConfig` | `cloud-config-v1.json` | Canonical cloud-init `cloudinit/config/schemas/schema-cloud-config-v1.json` (Draft-04) | `cue import -f -p schema -l '#CloudConfig:' -o cloud_config.cue jsonschema: sources/cloud-config-v1.json` |

To refresh a source, re-fetch the pinned upstream into `sources/`, re-run its
command, and re-run the full test suite (`go test ./...`) — the vendored schema's
`init()` registration compiles it at process start, so a broken conversion fails
fast.
