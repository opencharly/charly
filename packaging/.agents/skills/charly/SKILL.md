---
name: charly
description: 'OpenCharly CLI (charly) binary installed into container/VM images for
  in-container use.

  Use when working with charly binary deployment inside containers, in-container charly
  CLI usage, or the full charly toolchain (charly binary + virtualization + gocryptfs
  + socat).'
---

# charly -- OpenCharly CLI binary

## Candy Properties

| Property | Value |
|----------|-------|
| Install | native package from the published per-distro repos (`distro:` `package:`/`repo:`) |
| Binary source | the published release (charly-{alpine,arch,fedora,ubuntu,debian,openwrt}); the in-development binary lives in the `charly-dev` candy (check beds only) |

## What It Provides

The `charly` binary inside containers provides the full charly CLI for in-venue
scripting, service management, and automation. **The box need NOT bake the `charly`
candy for charly to run inside a venue:** when a flow needs `charly` present and the
venue lacks it (nested from-image delegation), the generic copy-`charly`-into-a-running-venue
mechanism (`EnsureCharlyInDeployVenue` over `DeployExecutor.PutFile` — `podman cp` for a
container, `scp` for a VM/host) copies the host's own binary in on demand and invokes
the delivered copy. Baking the candy only pre-stages the binary so the first such call
skips the copy.

## Binary source — the published per-distro package repos

The `charly` candy installs the binary from the **published per-distro package
repos** (charly-{alpine,arch,fedora,ubuntu,debian,openwrt}) via the `distro:`
`package:` + `repo:` entries above — the native package manager (apk add /
pacman -S / dnf install / apt install) pulls the release binary + its OS deps.
The `packaging:` section is the single metadata source those repos build from
(`charly generate-packages`, sdk/packagekit).

The IN-DEVELOPMENT binary (the local working tree, synced by
`scripts/bootstrap-charly.sh`) installs via the **`charly-dev`** candy — the local-source
variant used ONLY by charly check beds (`add_candy: [charly-dev]`), so a fresh
bed always exercises the code under development while a real box ships the
published release.

## Updating the Binary

**The commands in this section are maintenance the charly repository performs on itself.** They are not steps an charly user runs: the charly-dev candy's binary source is a working tree (see "Binary source" above), so this is something a contributor does inside a charly checkout. It is recorded here because it is this candy's own build contract — named as maintenance, not as instructions for the reader.

There is **one** path. The `charly-dev` candy is declared in the repo-root `charly.yml`, and a candy's `copy:` resolves against its declaring file's directory, so `copy: bin/charly` reads the repo-root `bin/charly` that `scripts/bootstrap-charly.sh` writes:

```bash
scripts/bootstrap-charly.sh                            # Builds repo-root bin/charly.
./bin/charly box build <image>               # Rebuild affected images.
```

Building with `go build -o ../bin/charly .` directly is equivalent for this purpose — the candy reads whatever is at repo-root `bin/charly`. There is no second copy to keep in step.

This used to be a two-path contract: charly-dev lived in `candy/charly-dev/`, so its `copy:` resolved against the candy directory and needed its own `bin/charly`, synced there by `scripts/bootstrap-charly.sh`. Forgetting that sync baked the PREVIOUS binary into every check bed — silently, because nothing failed. Declaring the candy where the path already resolves removed the second path, and the class of mistake with it.

**Still worth knowing**: `charly box build` uses auto-generated intermediate images (e.g., `ghcr.io/opencharly/charly-fedora-2-dbus-nodejs`) that cache the `charly-dev` candy. If an intermediate serves stale content, `charly clean --invalidate 'charly-fedora-2*'` forces a clean rebuild.

## `charly status` Probe

The `charly` probe checks:
1. Whether the `charly` binary exists in the container
2. The CalVer version (`charly version`)

Shows as `charly:ok (2026.94.1417)` in `charly status` detail view. Returns `-` for boxes without the `charly` candy.

**Note:** `charly version` writes to **stdout** via `fmt.Println` (the prior
`println(version)` emitted to stderr; the move to `fmt.Println` landed
with the MCP server work so the in-process tool-call path — which
captures `os.Stdout` — returns the CalVer correctly). The candy test at
`packaging/charly.yml` asserts `stdout:` matches `[0-9]{4}\.[0-9]+`. The
`charly status` probe uses `CombinedOutput()` so it's agnostic to the
stream.

## Usage

```yaml
# charly.yml — the charly candy is composed into all supervisord images
my-image:
  candy:
    base: fedora
    candy: [charly]
```

## Used In Boxes

- The `charly` candy is the full toolchain (charly binary + virtualization + gocryptfs + socat); composed into githubrunner, charly-fedora, charly-arch
- Now directly added to all boxes with supervisord (openclaw, jupyter, ollama, sway-browser-vnc, selkies-desktop, immich, etc.)

## Related Candies

- `/charly-infrastructure:virtualization`, `/charly-infrastructure:gocryptfs`, `/charly-infrastructure:socat` -- the candies the `charly` candy composes alongside the binary to form the full toolchain
- `/charly-coder:charly-mcp` -- candies: [charly, supervisord] meta-composition that deploys `charly mcp serve` (the MCP gateway) with a `/workspace` bind mount (volume NAME `project`) for build-mode tools + auto-fallback to opencharly/charly when nothing is bound

## When to Use This Skill

Use when the user asks about:

- Installing the charly binary inside containers
- The full charly toolchain composition (charly binary + virtualization + gocryptfs + socat)
- In-container charly CLI usage
- Updating the charly candy binary after code changes

## Related

- `/charly-image:layer` — candy authoring reference (`charly.yml` schema, plan-step verbs, service declarations)
- `/charly-check:check` — declarative testing (`check:` block, `charly check box`, `charly check live`)
