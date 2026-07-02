# charly/ — signpost (not the rule-set)

You are in the **Go CLI** source for `charly`.

**Load these skills FIRST (R0):**

- `/charly-internals:go` — source structure, building the `charly` binary, running
  tests, adding/modifying commands.
- `/charly-internals:install-plan` — the InstallPlan IR + DeployTarget /
  OCITarget pipeline (before touching `install_plan.go`, `install_build.go`,
  `build_target_oci.go`, `deploy_target_*.go`, `k8s_generate.go`,
  `k8s_deploy_preresolve.go`).
- `/charly-internals:capabilities` — the OCI-label capability contract.
- `/charly-internals:generate-source` — what `charly box generate` emits.
- Plus the renderer skills (`/charly-internals:vm-spec`, `cloud-init-renderer`,
  `libvirt-renderer`, `ovmf`) when touching VM/cloud-init code.

**Authoritative rules live in the repo-root `CLAUDE.md`** (one level up). R0–R10,
the hard-cutover policy, AI attribution, and the git-workflow are defined
there — this file only signposts and restates no rule. Go changes are R7/R8/R10
gated: `go build`/`go test` are cheap smoke, NOT the acceptance gate; the gate
is a live `charly check run <bed>` on a `disposable: true` target. History lives in
this repo's `CHANGELOG/` (one file per CalVer version).
