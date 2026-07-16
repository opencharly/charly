#!/usr/bin/env python3
"""Emit repository-level Codex R0 admission context for lifecycle hooks."""

from __future__ import annotations

import json
import sys


R0_MESSAGE = (
    "OpenCharly R0 admission: before any tool action, including read-only "
    "inspection, derive the matching dispatcher rows and read every matching "
    "repository skill completely. On-disk SKILL.md loading is the Codex "
    "equivalent when a skill is not registered. Do not act first and justify "
    "it later; an early action is an R0 violation."
)


def main() -> int:
    payload = json.load(sys.stdin)
    event = payload.get("hook_event_name")
    if event == "SessionStart":
        output = {
            "hookSpecificOutput": {
                "hookEventName": "SessionStart",
                "additionalContext": R0_MESSAGE,
            }
        }
    elif event == "PreToolUse":
        # Codex currently permits PreToolUse hooks to add a system message but
        # not to deny the call. AGENTS.md owns the binding behavioral rule.
        output = {"systemMessage": R0_MESSAGE}
    else:
        output = {"systemMessage": R0_MESSAGE}
    print(json.dumps(output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
