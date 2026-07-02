#!/usr/bin/env python3
"""Patch vLLM _decompose_size_nodes to handle getitem and x.size(dim) patterns.

Bug: vLLM's _decompose_size_nodes in compilation/backends.py assumes all
call_method target="size" nodes are full x.size() calls with users that
consume the entire tuple. It fails on:
  1. x.size(dim) calls (returns scalar, not tuple) - causes wrong dim count
  2. getitem(size, idx) users (index extraction) - splice produces malformed args
  3. size nodes used inside slice/tuple objects - identity check misses them

Fix: Skip x.size(dim) calls entirely (they already return a scalar).
Handle getitem users by replacing with dims[idx] directly.
Leave full-tuple users (view, reshape) with the existing splice logic.

Upstream: https://github.com/vllm-project/vllm/pull/38360 (not yet merged)
"""
import importlib.util
import operator
import sys
import textwrap
from pathlib import Path


def find_backends():
    spec = importlib.util.find_spec("vllm.compilation.backends")
    if spec is None or spec.origin is None:
        print("vllm.compilation.backends not found, skipping patch")
        sys.exit(0)
    return Path(spec.origin)


def patch():
    path = find_backends()
    src = path.read_text()

    # Check if already patched
    if "x.size(dim) returns a scalar" in src:
        print(f"Already patched: {path}")
        return

    # Part 1: Skip x.size(dim) calls that return a scalar
    old1 = "    for node in size_nodes:\n        tensor_node = node.args[0]"
    new1 = textwrap.dedent("""\
        for node in size_nodes:
            # x.size(dim) returns a scalar, not a torch.Size tuple -- skip
            if len(node.args) > 1:
                continue
            tensor_node = node.args[0]""").strip()
    # Fix indentation: the original uses 4-space indent at top level
    new1 = "    " + new1.replace("\n", "\n    ")

    if old1 not in src:
        print(f"WARNING: Could not find loop start block in {path}")
        print("The vLLM version may have changed. Skipping patch.")
        sys.exit(0)

    src = src.replace(old1, new1, 1)

    # Part 2: Replace user handling to support getitem pattern
    # Find the old block by its unique comment
    marker = "# Dynamo always passes size as a direct arg"
    if marker not in src:
        print(f"WARNING: Could not find user block marker in {path}")
        print("The vLLM version may have changed. Skipping patch.")
        sys.exit(0)

    # Build old and new blocks line by line
    arrow = "\u2192"
    old_lines = [
        "        # Replace size node in each user's args.",
        "        # Dynamo always passes size as a direct arg: view(clone, size)",
        f"        # {arrow} view(clone, d0, d1, ...)",
        "        for user in list(node.users):",
        "            new_args = []",
        "            for arg in user.args:",
        "                if arg is node:",
        "                    new_args.extend(dims)",
        "                else:",
        "                    new_args.append(arg)",
        "            user.args = tuple(new_args)",
        "        graph.graph.erase_node(node)",
    ]
    old_block = "\n".join(old_lines)

    new_lines = [
        "        # Replace size node in each user's args.",
        "        for user in list(node.users):",
        "            if (",
        '                user.op == "call_function"',
        "                and user.target is operator.getitem",
        "                and len(user.args) == 2",
        "                and user.args[0] is node",
        "            ):",
        f"                # getitem(size, idx) {arrow} replace with dims[idx] directly.",
        "                idx = user.args[1]",
        "                assert isinstance(idx, int), (",
        '                    f"Expected literal int index for getitem on size(), "',
        '                    f"got {type(idx).__name__}: {idx}"',
        "                )",
        "                user.replace_all_uses_with(dims[idx])",
        "                graph.graph.erase_node(user)",
        "            else:",
        "                # User consumes the full size tuple (e.g. view(clone, size))",
        f"                # {arrow} view(clone, d0, d1, ...)",
        "                new_args = []",
        "                for arg in user.args:",
        "                    if arg is node:",
        "                        new_args.extend(dims)",
        "                    else:",
        "                        new_args.append(arg)",
        "                user.args = tuple(new_args)",
        "        graph.graph.erase_node(node)",
    ]
    new_block = "\n".join(new_lines)

    if old_block not in src:
        print(f"WARNING: Could not find user replacement block in {path}")
        print("The vLLM version may have changed. Skipping patch.")
        sys.exit(0)

    src = src.replace(old_block, new_block, 1)

    # Ensure operator is imported
    if "import operator" not in src:
        src = "import operator\n" + src

    path.write_text(src)
    print(f"Patched: {path}")


if __name__ == "__main__":
    patch()
