---
title: Troubleshooting
description: Common symptoms and the first command to run — each entry points at the page that owns the detail.
sidebar:
  order: 4
---

First commands for the symptoms that come up most. Each row points at the page that owns the
detail; this table deliberately does not restate it.

## Deployments

| Symptom | First step |
|---|---|
| Service won't start | `charly status <image>`, then `charly logs <image>` — [status](/recipes/core/charly-status/), [logs](/recipes/core/logs/) |
| Quadlet out of sync with `charly.yml` | `charly config <image> --update-all` — [config](/recipes/core/charly-config/) |
| Service built fine but is broken in production | `charly check live <image>` runs the baked plan against the running deployment — [check](/recipes/check/check/) |
| `charly fleet add vm:<name>` errors "VM does not exist" | Run `charly vm create <name>` first — VM deploy does not auto-provision. [deploy](/recipes/core/deploy/) |
| Tunnel missing on a new instance | Tunnel config is `charly.yml`-only and is not inherited per instance — add it explicitly. [deploy](/recipes/core/deploy/) |
| Encrypted volume locked at boot | `charly config mount` waits for keyring unlock automatically — [enc](/recipes/automation/enc/) |

## Builds

| Symptom | First step |
|---|---|
| Build cache stale | `charly box build --no-cache <image>` — [build](/recipes/build/build/) |
| `charly box pull` says "image is not available locally" | `box pull` accepts a short name, a fully-qualified ref, or an `@github` remote ref — [pull](/recipes/build/pull/) |
| Resolver warns "referenced at multiple versions" | `charly box reconcile` aligns the cross-repo pins — [reconcile](/recipes/build/reconcile/). A warning is never an acceptable end state; see [reproducible, not merely successful](/concepts/08-reproducible-not-merely-successful/) |
| No packages installed, `"Distro": null` in `box inspect` | An external `base:` does not inherit distro tags — declare `distro:` explicitly on the box. [image](/recipes/image/image/) |
| Newer-than-binary config rejected at load | `charly migrate` brings the project to the current schema — [migrate](/recipes/build/migrate/) |
| A schema or format change won't apply | `charly migrate` is idempotent and is auto-invoked on remote-cache fetches |

## Host and hardware

| Symptom | First step |
|---|---|
| GPU not detected | `charly doctor`, then [udev](/recipes/automation/udev/) for the host-side rules |
| Chrome stuck or crash-looping | See the resource-caps and circuit-breaker section of [chrome](/recipes/selkies/chrome/) |
| SPICE console blank on a cloud-image VM | Known `simpledrm → qxldrmfb` race under UEFI; switch to `firmware: bios` — [arch-cloud-vm](/recipes/vm/arch-cloud-vm/) |
| Missing host dependency | `charly doctor` checks podman/docker, libvirt, qemu, gnupg, gocryptfs, tailscale and friends — [doctor](/recipes/core/charly-doctor/) |

## When the symptom is not listed

Two habits are worth more than any table.

**Check the binary first.** A stale `charly` fails in ways that look like real bugs. Run
`charly version` and confirm it is the build you meant to be running — if you installed the
package, that it matches the package you installed, and if two copies are on `$PATH`, which one
`which charly` actually resolves to.

**Let the box tell you what it claims.** Every image carries its own acceptance plan as an OCI
label, so `charly check box <image>` and `charly check live <image>` will often name the broken
assumption directly rather than leaving you to guess. That is what the plan is for — see
[the spec is the test](/concepts/06-the-spec-is-the-test/).
