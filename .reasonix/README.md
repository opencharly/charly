# Running charly under reasonix

This page explains why podman fails for a reasonix agent running charly, and how
this repository's `reasonix.toml` fixes it. It is the full-RCA companion to the
comment in `reasonix.toml` — write future mechanism explanations to this file,
not to prose scattered through config comments.

## Symptom

charly's every podman-backed operation — `charly box build`, `charly check box`,
`charly check live`, `charly fleet add`, deploy, fresh `charly update` — works in
a normal operator terminal but fails when driven by a reasonix agent, with one or
both of:

```
Error: creating container storage: mount ...: read-only file system   (on /run/user/1000)
newuidmap: Could not set caps
```

The failure looks "environmental" and unfixable from inside charly: the exact
same charly binary, config, and image work by hand. That contrast — "works in a
terminal, fails for the agent" — is the diagnostic signature. It is the harness
sandbox, not the host.

## Root cause

Reasonix's OS-level Bash sandbox (the global `~/.reasonix/config.toml`
`[sandbox] bash = "enforce"` setting) jails every agent Bash call. The jail:

- sets `NoNewPrivs=1` and empties the capability bounding set
  (`CapBnd: 0000…0000`),
- drops the process into a nested user namespace
  (`uid_map 1000 0 1`),
- remounts `/run` and `$HOME` read-only.

Rootless podman — which charly uses for every box build/check/deploy op —
requires **both** a writable runroot (a subdir of `/run`, used for the storage
graphroot and the runtime) **and** the `newuidmap`/`newgidmap`
subordinate-id-mapping capabilities that let it map container user namespaces.
The jail strips both, so podman fails only for the agent while a normal operator
terminal (no jail) runs it fine.

The earlier "environmental, unfixable" conclusion was wrong: it measured the
sandbox, not the host.

## Fix

The project-scoped override in `reasonix.toml`:

```toml
[sandbox]
bash = "off"
```

Resolution order is `flag > ./reasonix.toml > ~/.reasonix/config.toml`, so this
project override wins over the global jail and runs the project's agent Bash
commands on the real host — like an operator terminal — restoring podman.

The override is project-scoped: it does not change the operator's global reasonix
security posture, and any other project still inherits the global `enforce` jail.
It applies on the next reasonix session (the jail is configured at session
launch).

## Verification

From the fix in effect, an agent session must observe:

- `/proc/self/status` → `NoNewPrivs: 0` and a full `CapBnd` (not `0000…`),
- `/proc/self/uid_map` → the root mapping (no nested namespace),
- `/run` mounted `rw` for the invoking user, with `/run/user/<uid>` writable,
- `podman run --rm alpine:latest echo ok` printing `ok`,
- `./bin/charly box validate` exiting 0.

Any of these failing means the session is still jailed — the override did not
take effect (it requires a fresh session launch) or a lower-precedence config
won.

## Scope boundary

This fixes the *sandbox*; it does not grant extra permissions. The `[permissions]`
allow-list and `mode` in `reasonix.toml` still gate every command as before.
Disabling the sandbox trades OS-level command confinement for the repository's
declared permission policy, which is the intended trade for a project that runs
podman-backed container engines.
