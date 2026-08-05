#!/usr/bin/env python3
"""Verify every fenced YAML block in the hand-authored pages.

The rule this enforces: a block is EITHER a verbatim excerpt of a committed source
file, OR a deliberately synthetic template the prose frames as one ("write it
directly", "declare a bed"). Nothing else is allowed.

Why it exists in this shape: an earlier version keyed blocks by their top-level
name and did `if not owner: continue`. That silently skipped 9 of 15 blocks —
including one that quoted a `plan:` which had never existed in any revision — while
reporting a confident "116/116 verify verbatim". A checker that skips what it does
not recognise reports success on what it never checked. So an unclassified block is
now a FAILURE, not a skip, and the counts below are of blocks, not just lines.

It lives in the SUPERPROJECT, not in `docs/`, because it reads sources from three
repos at once — the superproject's `README.md`, `candy/*` here, `box/fedora/*` in
the distro submodule, and the pages in the `docs/` submodule. Only a superproject
checkout has all of them, so a copy inside `docs/` could never run from that
repo's own clone.

KNOWN LIMITS — do not read a green run as "the docs are consistent":

1. It verifies against the LOCAL WORKING TREE, not merged upstream. A page quoting a
   box that only exists on an unmerged feat branch passes here and is wrong for a
   reader. Check the land order separately.
2. It only reads fenced ```yaml blocks. Stale PROSE — "composes three candies", a
   flag explanation left behind by a command sweep — is invisible to it.
3. A block whose top key is a fragment (e.g. `plan:`) cannot be attributed to a
   source, so it is declared SYNTHETIC and 0 of its lines are checked, even when the
   prose around it calls it a quote.

Usage: python3 scripts/verify-doc-excerpts.py    (run from the superproject root)
"""
import glob
import os
import re
import sys

# Blocks that must match a committed source file, byte for byte (modulo elisions).
EXCERPT_SOURCES = {
    "ripgrep": "candy/ripgrep/charly.yml",
    "tutorial-shell": "box/fedora/box/tutorial-shell/charly.yml",
    "check-tutorial-shell": "box/fedora/charly.yml",
    # check-group: the real nested local:-inside-a-disposable-VM bed the quickstart and
    # concepts/02 quote. It replaced `check-docs-local`, which those pages presented as this
    # repository's own configuration while it existed only on an unlanded branch — the
    # fabrication this checker exists to catch. Its entry is removed with it; a source map
    # naming a bed that is not in charly.yml is the same lie one level down.
    "check-group": "charly.yml",
    "plugin-example": "candy/plugin-example/charly.yml",
}

# Blocks the prose explicitly presents as templates for the reader's OWN project.
# Each must be justified here by name; adding one is a deliberate act, not a default.
SYNTHETIC = {
    "my-shell": "authoring-a-candy: 'or write it directly' — the reader's own box",
    "check-my-shell": "authoring-a-candy: the reader's own disposable bed",
    "my-plugin": "authoring-a-plugin: the reader's own plugin candy",
    "package": "authoring-a-candy: a per-distro package cascade fragment",
    "plan": "the-spec-is-the-test: a plan fragment quoted from ripgrep, shown headless",
    "plugin": "authoring-a-plugin: a bare `plugin:` block fragment, no owning entity",
}

PAGES = (
    ["README.md"]
    + sorted(glob.glob("docs/src/content/docs/concepts/*.md"))
    + [
        "docs/src/content/docs/start/quickstart.md",
        "docs/src/content/docs/guides/authoring-a-candy.md",
        "docs/src/content/docs/guides/authoring-a-plugin.md",
        # The landing page is the generated index.md, a projection of README.md — which is
        # already in PAGES above, so the landing page's YAML is checked via its source.
    ]
)


def top_key(block):
    """First non-comment, non-blank line's mapping key."""
    for line in block.splitlines():
        s = line.strip()
        if not s or s.startswith("#"):
            continue
        m = re.match(r"([A-Za-z0-9_.-]+):", s)
        return m.group(1) if m else None
    return None


def main():
    bodies = {}
    for name, path in EXCERPT_SOURCES.items():
        if not os.path.exists(path):
            print(f"FAIL: source file missing: {path}")
            return 1
        bodies[name] = open(path, encoding="utf8").read()

    blocks = excerpts = synthetic = 0
    lines_checked = 0
    failures = []

    for page in PAGES:
        if not os.path.exists(page):
            failures.append(f"{page}: page missing")
            continue
        for block in re.findall(r"```yaml\n(.*?)```", open(page, encoding="utf8").read(), re.S):
            blocks += 1
            key = top_key(block)
            if key in EXCERPT_SOURCES:
                excerpts += 1
                for line in block.splitlines():
                    s = line.strip()
                    # '...' marks a deliberate elision; comments are page-local annotation
                    if not s or s.startswith("#") or s.endswith("..."):
                        continue
                    lines_checked += 1
                    if s not in bodies[key]:
                        failures.append(f"{page}: not in {EXCERPT_SOURCES[key]}: {s[:90]}")
            elif key in SYNTHETIC:
                synthetic += 1
            else:
                # The whole point: never skip silently.
                failures.append(
                    f"{page}: UNCLASSIFIED block (top key {key!r}) — declare it in "
                    f"EXCERPT_SOURCES or SYNTHETIC"
                )

    print(f"fenced yaml blocks : {blocks}")
    print(f"  excerpts         : {excerpts}  ({lines_checked} lines checked against source)")
    print(f"  synthetic        : {synthetic}  (declared templates)")
    print(f"  unclassified     : {blocks - excerpts - synthetic}  (must be 0)")
    if failures:
        print(f"\nFAILURES ({len(failures)}):")
        for f in failures:
            print(f"  {f}")
        return 1
    print("\nOK: every block is either a verified excerpt or a declared template.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
