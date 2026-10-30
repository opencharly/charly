#!/usr/bin/env python3
"""check-pin-verb-scope.py — the control over THIS repo's own pin-advance claim.

WHAT IT ASKS, over this repo's own tracked tree:

  A. SCOPE — every claim-maker surface states the verb's SCOPE: the advance
     touches EVERY pin this repo declares. The marker is a CLAIM, not a keyword:
     "every" on its own also matches a question about CURRENCY ("asks whether
     every pin is at the ref its intent names"), so the marker carries the whole
     claim — every … pin(s) … declares.
  B. NO RETIRED CLAIM — no tracked file calls the pin ADVANCE a non-mutating
     kind (a preview, a dry run, a read-only report). The claim is FALSE: the
     advance rolls the checkouts that exist and stages their gitlinks
     (opencharly/charly#1079). The wording that was here described the `sync`
     task as "preview only; does not commit" — half true: it does not commit,
     and it is not a preview.
  C. COVERAGE — every tracked file that NAMES the pin-advance verb is classified
     here: a claim-maker file (its surfaces are listed below), a non-claim
     mention (with the reason it is not a claim-maker), or an exempt control. A
     file that names the verb and is in NEITHER list is a FINDING: a new
     claim-maker surface cannot slip in unclassified.

  D. ONE HOME — the shared rule (`scripts/tracked-files.txt`) states its GRAMMAR and
     its exempt-mode LIST in its OWN header, and nowhere else: a copy of either into
     a reader is a second rule, and two rules drift (R2). And every reader the rule
     file NAMES still READS that file — a reader that stopped reading it would parse
     a format the rule file no longer specifies, and the two gates would disagree
     about the file's syntax without anything saying so.

And a surface that MOVED is a finding too, not a silent pass. Every surface is
located by a STRUCTURAL anchor — a chain of keys (the `sync:` task, the `MODE:`
parameter) and the LINE KIND inside them, never the claim's own wording — so the
control's own mutation cannot make a surface vanish instead of failing, and a
surface that moves is a finding rather than a silent pass. A gate that cannot see
the surface it is checking is worse than no gate.

WHY IT LIVES HERE, in this repo and not in the umbrella: the umbrella's own gate
for the same rule (opencharly/opencharly's scripts/check-pin-verb-scope.mjs)
states, in its own header, that it speaks for the UMBRELLA's prose only — "a
submodule is a repo of its own, with its own rulebook and its own gates". This
repo declares its own box/* pins and its own `sync` task, so it owns the claim
about them; the umbrella records only the gitlink. The umbrella's #501 leg fixed
the same claim in the umbrella's tree and filed this repo's twin as
opencharly/charly#1079.

usage: python3 -I scripts/check-pin-verb-scope.py [--root <dir>] [--self-test]
exit 0 clean · 1 finding
"""

import argparse
import os
import re
import subprocess
import sys

SELF_REL = "scripts/check-pin-verb-scope.py"

# The ONE rule for what this repository's tracked tree contains: the index modes
# that are EXEMPT from it. It is a committed FILE, and BOTH readers of the tracked
# tree read it — this gate and charly/retired_build_selector_test.go — so the
# exemptions have ONE home and the two gates cannot drift on WHICH modes are exempt
# (R2). Each reader parses that file in its own language, so a copy of the LIST in
# a reader would be a second list, and two lists drift. The file's own header
# specifies the grammar; read it there, never copy it here.
TRACKED_FILES_RULE = "tracked-files.txt"

# The rule file's own PATH in this tree — the ONE place the shared rule lives.
RULE_REL = "scripts/" + TRACKED_FILES_RULE

# The GRAMMAR's marker: the phrase the rule file's own header uses to say how a
# line of the rule file is written. It is assembled from parts, so this control's
# own SOURCE never carries the phrase — a control that flagged itself would be a
# gate that can never be green.
GRAMMAR_MODE = "<mode>"
GRAMMAR_REASON = "<reason>"
GRAMMAR_RE = re.compile(r"`%s\s+%s`\s+per line" % (GRAMMAR_MODE, GRAMMAR_REASON))
GRAMMAR_PHRASE = "`%s %s` per line" % (GRAMMAR_MODE, GRAMMAR_REASON)

# The rule file's paragraphs, located by their OWN HEADINGS — a structural anchor,
# never the grammar's own wording, so a copy that MOVES cannot make this arm blind
# instead of red.
GRAMMAR_PARAGRAPH_RE = re.compile(r"^#\s*WHAT IS EXEMPT\b")
READERS_PARAGRAPH_RE = re.compile(r"^#\s*THE READERS\b")
BACKTICKED_RE = re.compile(r"`([^`]+)`")

# A reader READS the rule file when its CODE names the file as a string literal.
# A prose mention of the rule file is a mention, not a read.
QUOTED_RULE_RE = re.compile(r"""['"]%s['"]""" % re.escape(TRACKED_FILES_RULE))

# The SCOPE claim: the verb advances EVERY pin this repo declares.
#
# `[\s\S]` (not `[^\n]`) because a claim WRAPS: the scope sentence runs over a
# line boundary in the comment and bullet surfaces, and a marker that cannot
# cross a wrap would read a stated scope as an absent one. The windows are
# small (30 and 40 characters), so the match stays inside a sentence or two.
#
# The VERB after the pin word is what separates the scope claim from the
# CURRENCY question: "asks whether every pin is at the ref its intent names" is
# not a statement of what the advance advances, and no verb in the list follows
# that sentence's "pin".
SCOPE_RE = re.compile(
    r"\bevery\b[\s\S]{0,30}\bpin(?:s)?\b[\s\S]{0,40}"
    r"\b(?:declares?|declared|declaring|records?|recorded|holds|moves|moved|advances|advanced)\b",
    re.I,
)

# The RETIRED claim: the advance is a non-mutating kind. The shape is the
# umbrella's own class (opencharly/opencharly's scripts/check-retired-claims.mjs),
# scoped to this repo's tree: a non-mutating word and the advance word within
# 80 characters of each other, in either order.
RETIRED_CLAIM_RE = re.compile(
    r"(?:\(\s*(?:preview|dry[ -]?run|read[ -]?only)\b"
    r"|\b(?:is|are|as|its)\s+(?:an?\s+)?(?:preview|dry[ -]?run|read[ -]?only)\b)"
    r"[^\n]{0,80}?\b(?:bump(?:s|ed|ing)?|advanc(?:e|es|ed|ing))\b"
    r"|"
    r"\b(?:bump(?:s|ed|ing)?|advanc(?:e|es|ed|ing))\b[^\n]{0,80}?"
    r"(?:\(\s*(?:preview|dry[ -]?run|read[ -]?only)\b"
    r"|\b(?:is|are|as|its)\s+(?:an?\s+)?(?:preview|dry[ -]?run|read[ -]?only)\b)",
    re.I,
)

# The verb's own spellings — a file that NAMES it is a file the coverage arm
# classifies.
VERB_RE = re.compile(r"charly task sync|charly task pins|MODE=bump", re.I)

# The claim-maker surfaces: the places whose job is to tell an operator what
# the pin-advance verb does, so the operator can decide whether to run it. Each
# states the scope (assertion A), and each is located by a STRUCTURAL chain:
#
#   "chain" — the keys that narrow to the surface's region. Each key opens a
#             region that ends at the next non-blank line at the SAME indent.
#   "within" — the line kind inside that region (a task description, a step
#             name, a parameter description). Omitted for a markdown surface,
#             which is the bullet itself.
#
# The anchors carry no claim wording, so editing the claim cannot move a
# surface out of the gate's sight.
SURFACES = [
    {
        "id": "charly.yml — the sync task's header comment",
        "rel": "charly.yml",
        "kind": "comment",
        "chain": [re.compile(r"^# sync — the named entry point for the advance half")],
        "within": None,
    },
    {
        "id": "charly.yml — the sync task's description",
        "rel": "charly.yml",
        "kind": "line",
        "chain": [re.compile(r"^sync:$")],
        "within": re.compile(r"^\s{4}description:"),
    },
    {
        "id": "charly.yml — the sync task's step name",
        "rel": "charly.yml",
        "kind": "line",
        "chain": [re.compile(r"^sync:$")],
        "within": re.compile(r"^\s+- run:"),
    },
    {
        "id": "charly.yml — the pins task's description",
        "rel": "charly.yml",
        "kind": "line",
        "chain": [re.compile(r"^pins:$")],
        "within": re.compile(r"^\s{4}description:"),
    },
    {
        "id": "charly.yml — the pins task's MODE parameter",
        "rel": "charly.yml",
        "kind": "line",
        "chain": [re.compile(r"^pins:$"), re.compile(r"^\s{6}MODE:$")],
        "within": re.compile(r"^\s{8}description:"),
    },
    {
        "id": "AGENTS.md — the sanctioned-advance bullet",
        "rel": "AGENTS.md",
        "kind": "block",
        "chain": [re.compile(r"^- \*\*The only sanctioned advance is `charly task sync`\*\*")],
        "within": None,
    },
    {
        "id": "AGENTS.md — the MODE parameter bullet",
        "rel": "AGENTS.md",
        "kind": "block",
        "chain": [re.compile(r"^- \*\*The bound is a parameter, not a constant in the rule\.\*\*")],
        "within": None,
    },
]

# The files that NAME the pin-advance verb without PRESENTING it. The reason is
# committed here, because "this file is not a claim-maker" is a judgement, and a
# judgement with no stated reason is a judgement nobody can check.
NON_CLAIM_MENTIONS = {}

# The files exempt from the coverage arm, with the reason.
EXEMPT_FILES = {
    SELF_REL: "this gate itself — a control names the claim it checks",
}

LIST_ITEM_RE = re.compile(r"^\s*(?:[-*+]|\d+[.)])\s+")


class GateError(RuntimeError):
    """The control could not JUDGE the tree. It never passes on a tree it could
    not read — a gate that cannot see the claim is worse than no gate."""


def indent_of(line):
    return len(line) - len(line.lstrip())


def region_end(lines, idx, limit):
    """The end of the region a key at `idx` opens: the next non-blank line at the
    SAME indent (or shallower) — a sibling or an ancestor key."""
    ind = indent_of(lines[idx])
    j = idx + 1
    while j < limit:
        line = lines[j]
        if line.strip() and indent_of(line) <= ind:
            return j
        j += 1
    return limit


def locate(lines, surface):
    """The surface's line index, or (None, why).

    Every key in the chain must match EXACTLY ONE line inside the region the
    previous key opened; a key that matches none means the surface moved (a
    finding), and a key that matches several means the surface is ambiguous
    (also a finding)."""
    start, end = 0, len(lines)
    for key in surface["chain"]:
        hits = [i for i in range(start, end) if key.search(lines[i])]
        if not hits:
            return None, "its key matches no line"
        if len(hits) > 1:
            return None, "its key matches %d lines (%s)" % (
                len(hits),
                ", ".join(str(h + 1) for h in hits),
            )
        idx = hits[0]
        start, end = idx + 1, region_end(lines, idx, end)

    within = surface["within"]
    if within is None:
        idx = hits[0]
        return idx, None

    hits = [i for i in range(start, end) if within.search(lines[i])]
    if not hits:
        return None, "no line of that kind inside it"
    if len(hits) > 1:
        return None, "%d lines of that kind inside it (%s)" % (
            len(hits),
            ", ".join(str(h + 1) for h in hits),
        )
    return hits[0], None


def extract(lines, idx, kind):
    """The surface's text: one line, a comment block, or a list item with its
    continuation lines.

    A list item CONTINUES on the lines indented to its content column, and a
    claim that wraps inside a list item is one claim — so the next item at the
    anchor's own indent ENDS the surface."""
    if kind == "line":
        return lines[idx]
    out = [lines[idx]]
    if kind == "comment":
        for k in range(idx + 1, len(lines)):
            if not re.match(r"^\s*#", lines[k]):
                break
            out.append(lines[k])
        return "\n".join(out)
    ind = indent_of(lines[idx])
    is_item = bool(LIST_ITEM_RE.match(lines[idx]))
    next_item = re.compile(r"^\s{0,%d}(?:[-*+]|\d+[.)])\s" % ind)
    for k in range(idx + 1, len(lines)):
        line = lines[k]
        if line.strip() == "":
            break
        if is_item and next_item.match(line):
            break
        out.append(line)
    return "\n".join(out)


def rule_paragraph(lines, anchor):
    """The comment paragraph a structural anchor opens: (text, first_line, last_line).

    A paragraph ends at the next bare comment marker, or where the comment block
    ends. The anchor is the paragraph's OWN HEADING, so a copy that MOVES cannot
    make this arm blind: an anchor that matches no paragraph — or more than one —
    is a FINDING, never an empty pass."""
    hits = [i for i, line in enumerate(lines) if anchor.search(line)]
    if len(hits) != 1:
        return None
    start = hits[0]
    end = start + 1
    while end < len(lines) and lines[end].startswith("#") and lines[end].strip() != "#":
        end += 1
    return ("\n".join(lines[start:end]), start + 1, end)


def tracked_files_rule():
    """The index modes that are EXEMPT from this repository's tracked tree — the
    ONE rule, read from the committed `scripts/tracked-files.txt` that the
    retired-knob surface control reads too, so the exemptions have ONE home and the
    two gates cannot drift on WHICH modes are exempt (R2)."""
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), TRACKED_FILES_RULE)
    try:
        with open(path, "r", encoding="utf-8") as fh:
            raw = fh.read()
    except OSError as exc:
        raise GateError("cannot read the tracked-files rule %s: %s" % (path, exc))
    modes = set()
    for line in raw.split("\n"):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split()
        if not fields:
            continue
        modes.add(fields[0])
    if not modes:
        raise GateError(
            "the tracked-files rule %s declares no exempt mode: the tracked set would widen "
            "silently, and a gitlink's checkout is not this repo's prose" % path
        )
    return modes


def tracked_files(root):
    """The tracked REGULAR files of a tree — derived from the INDEX, never
    hand-kept. The modes that are exempt from it come from the ONE committed
    rule (`scripts/tracked-files.txt`), which the retired-knob surface control
    reads too, so the exemptions have ONE home and the two gates cannot drift on
    WHICH modes are exempt (R2)."""
    try:
        raw = subprocess.run(
            ["git", "ls-files", "-s", "-z"],
            cwd=root,
            capture_output=True,
            check=True,
        ).stdout.decode("utf-8", "replace")
    except (subprocess.CalledProcessError, OSError) as exc:
        raise GateError("cannot read the index of %s: %s" % (root, exc))
    exempt = tracked_files_rule()
    out = []
    for rec in raw.split("\0"):
        if not rec:
            continue
        tab = rec.find("\t")
        if tab == -1:
            continue
        mode = rec[:tab].split(" ")[0]
        if mode in exempt:
            continue
        out.append(rec[tab + 1:])
    return out


def read_text(abs_path):
    """A file's text, or None when it is unreadable or binary."""
    try:
        with open(abs_path, "rb") as fh:
            buf = fh.read()
    except OSError:
        return None
    if b"\0" in buf:
        return None
    return buf.decode("utf-8", "replace")


def findings(root, overrides=None, non_claim_mentions=None, exempt_files=None, tracked=None):
    """The findings over the files the run sees: the tree's tracked files, with
    `overrides` replacing or adding one (the --self-test's mutations). `tracked`
    overrides the tracked set itself (the --self-test's mutation of the SCOPE)."""
    overrides = overrides or {}
    non_claim_mentions = NON_CLAIM_MENTIONS if non_claim_mentions is None else non_claim_mentions
    exempt_files = EXEMPT_FILES if exempt_files is None else exempt_files
    out = []

    def text_of(rel):
        if rel in overrides:
            return overrides[rel]
        return read_text(os.path.join(root, rel))

    tracked = tracked_files(root) if tracked is None else tracked

    # The SCOPE arm: the tracked tree this control judges must CONTAIN every
    # claim-maker surface. A surface that is not tracked is a surface no reader
    # of this repository sees, and a rule whose scope silently stopped covering
    # it is a green light that proves nothing.
    for surface in SURFACES:
        rel = surface["rel"]
        if rel not in tracked:
            out.append(
                (
                    "B",
                    rel,
                    0,
                    "%s — the surface is not a TRACKED file: the control's scope does not "
                    "cover the tree a reader of this repository sees" % surface["id"],
                )
            )

    # A + the EXISTENCE arm: every claim-maker surface exists, is unique, and
    # states the scope.
    for surface in SURFACES:
        rel = surface["rel"]
        text = text_of(rel)
        if text is None:
            out.append(("B", rel, 0, "%s — the file is unreadable" % surface["id"]))
            continue
        lines = text.split("\n")
        idx, why = locate(lines, surface)
        if idx is None:
            out.append(
                (
                    "B",
                    rel,
                    0,
                    "%s — the surface is GONE or ambiguous: %s. A surface that moves is a "
                    "finding, not a silent pass" % (surface["id"], why),
                )
            )
            continue
        claim = extract(lines, idx, surface["kind"])
        if not SCOPE_RE.search(claim):
            out.append(
                (
                    "A",
                    rel,
                    idx + 1,
                    "%s — the surface does not state the verb's SCOPE: it must say the verb "
                    "advances EVERY pin this repo declares (the claim, not the keyword: "
                    '"every" alone also matches a question about currency). A reader with no '
                    "scope runs a wave they thought was a report" % surface["id"],
                )
            )

    # B: no tracked file calls the advance a non-mutating kind.
    for rel in tracked:
        if rel.startswith("CHANGELOG/") or rel == SELF_REL:
            continue  # history, not a current claim; and this gate carries the class on purpose
        text = text_of(rel)
        if text is None:
            continue
        for lineno, line in enumerate(text.split("\n"), 1):
            if RETIRED_CLAIM_RE.search(line):
                out.append(
                    (
                        "B",
                        rel,
                        lineno,
                        "%s:%d: the pin ADVANCE is called a non-mutating kind — it advances "
                        "EVERY declared pin, rolls the checkouts that exist and stages their "
                        "gitlinks (opencharly/charly#1079): %s"
                        % (rel, lineno, line.strip()[:160]),
                    )
                )

    # C: coverage — every file that NAMES the verb is classified.
    claim_makers = set(s["rel"] for s in SURFACES)
    files = set(tracked)
    for rel in overrides:
        files.add(rel)
    for rel in sorted(files):
        if rel.startswith("CHANGELOG/"):
            continue
        if rel in claim_makers or rel in non_claim_mentions or rel in exempt_files:
            continue
        text = text_of(rel)
        if text is None or not VERB_RE.search(text):
            continue
        out.append(
            (
                "C",
                rel,
                0,
                "%s names the pin-advance verb and is not classified: it is neither a "
                "claim-maker file (whose surfaces are listed in this gate), nor a non-claim "
                "mention (with a reason), nor an exempt control. A new surface cannot slip "
                "in unclassified" % rel,
            )
        )

    # And the classification itself cannot go stale: a file listed as a non-claim
    # mention must still name the verb, or the reason describes a mention that is
    # gone.
    for rel in non_claim_mentions:
        text = text_of(rel)
        if text is None or not VERB_RE.search(text):
            out.append(
                (
                    "C",
                    rel,
                    0,
                    "%s is classified as a non-claim mention, but it no longer names the "
                    "verb — the reason describes a mention that is gone" % rel,
                )
            )


    # D: the shared rule has ONE home. Its GRAMMAR and its exempt-mode LIST live in
    # its own header, and every reader it NAMES still reads it. A copy of either
    # into a reader is a second rule, and two rules drift (R2).
    rule_text = text_of(RULE_REL)
    if rule_text is None:
        out.append(
            (
                "D",
                RULE_REL,
                0,
                "the shared rule file is unreadable — its shape was NOT judged",
            )
        )
    else:
        rule_lines = rule_text.split("\n")
        grammar_span = None
        para = rule_paragraph(rule_lines, GRAMMAR_PARAGRAPH_RE)
        if para is None:
            out.append(
                (
                    "D",
                    RULE_REL,
                    0,
                    "the rule file has no single 'WHAT IS EXEMPT' paragraph — the grammar's "
                    "home cannot be located, and a gate that cannot see it proves nothing",
                )
            )
        else:
            grammar_span = (para[1], para[2])
            if not GRAMMAR_RE.search(para[0]):
                out.append(
                    (
                        "D",
                        RULE_REL,
                        para[1],
                        "the rule file's own header does not state the GRAMMAR any more — the "
                        "one home of the rule's syntax is empty, and every reader would be "
                        "parsing a format nothing specifies",
                    )
                )

        # The GRAMMAR, site by site: one, in the rule file's own header.
        for rel in tracked:
            if rel.startswith("CHANGELOG/") or rel == RULE_REL:
                continue
            text = text_of(rel)
            if text is None:
                continue
            for lineno, line in enumerate(text.split("\n"), 1):
                if GRAMMAR_RE.search(line):
                    out.append(
                        (
                            "D",
                            rel,
                            lineno,
                            "the shared rule's GRAMMAR is restated here — it is specified ONCE, "
                            "in %s's own header, and a copy of it in a reader is a second rule "
                            "(R2)" % RULE_REL,
                        )
                    )

        # And inside the rule file itself, the grammar may live ONLY in that header.
        if grammar_span is not None:
            for lineno, line in enumerate(rule_lines, 1):
                if not GRAMMAR_RE.search(line):
                    continue
                if grammar_span[0] <= lineno <= grammar_span[1]:
                    continue
                out.append(
                    (
                        "D",
                        RULE_REL,
                        lineno,
                        "the shared rule's GRAMMAR is restated OUTSIDE its own header — the "
                        "header is its one home, and a second statement of the same rule in "
                        "the same file is two rules (R2)",
                    )
                )

        # The exempt-mode LIST, site by site: one, in the rule file. A copy is a copy
        # of the LIST — the mode AND its reason — not a prose mention of a number.
        listed = []
        for line in rule_lines:
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            fields = stripped.split()
            if len(fields) < 2:
                continue
            listed.append((fields[0], " ".join(fields[1:])))
        if not listed:
            out.append(
                (
                    "D",
                    RULE_REL,
                    0,
                    "the rule file declares no exempt mode — the tracked set would widen "
                    "silently, and a gitlink's checkout is not this repo's prose",
                )
            )
        for mode, reason in listed:
            for rel in tracked:
                if rel.startswith("CHANGELOG/") or rel == RULE_REL:
                    continue
                text = text_of(rel)
                if text is None:
                    continue
                for lineno, line in enumerate(text.split("\n"), 1):
                    flat = " ".join(line.split())
                    if mode not in flat.split():
                        continue
                    if reason not in flat:
                        continue
                    out.append(
                        (
                            "D",
                            rel,
                            lineno,
                            "the shared rule's exempt-mode LIST is restated here — the modes "
                            "live in %s, and a copy of the list in a reader is a second list, "
                            "and two lists drift (R2)" % RULE_REL,
                        )
                    )

        # And every reader the rule file NAMES still READS it.
        readers = rule_paragraph(rule_lines, READERS_PARAGRAPH_RE)
        if readers is None:
            out.append(
                (
                    "D",
                    RULE_REL,
                    0,
                    "the rule file has no single 'THE READERS' paragraph — the readers cannot "
                    "be located, and a gate that cannot see them proves nothing",
                )
            )
        else:
            paths = [p for p in BACKTICKED_RE.findall(readers[0]) if "/" in p]
            if not paths:
                out.append(
                    (
                        "D",
                        RULE_REL,
                        readers[1],
                        "the rule file's readers paragraph names no path — the readers of the "
                        "shared rule are not derivable",
                    )
                )
            for named in paths:
                if named not in tracked:
                    out.append(
                        (
                            "D",
                            RULE_REL,
                            readers[1],
                            "the rule file names %s as a reader, but it is not a tracked file "
                            "of this tree" % named,
                        )
                    )
                    continue
                text = text_of(named)
                if text is None or not QUOTED_RULE_RE.search(text):
                    out.append(
                        (
                            "D",
                            named,
                            0,
                            "%s is named as a reader of the shared rule, but its code does not "
                            "name the rule file — it stopped reading the rule, and it now "
                            "parses a format the rule file no longer specifies" % named,
                        )
                    )

    return out


def report(found):
    for kind, rel, line, msg in found:
        where = "%s:%d: " % (rel, line) if line else "%s: " % rel
        print("  FAIL  [%s] %s%s" % (kind, where, msg), file=sys.stderr)
    return len(found)


def self_test(root):
    """Prove every arm goes RED on a mutation of the shipped text — and that the
    shipped tree is GREEN. A control that cannot fail is a green light that
    proves nothing."""
    failures = []
    arms = []

    def check(name, ok_flag, detail=""):
        # The arm is COUNTED here, not written as a number in the summary: an
        # arm count that a later edit has to update by hand is a claim nothing
        # checks, and this gate exists to keep claims honest.
        arms.append(name)
        if ok_flag:
            print("  PASS  %s" % name)
        else:
            print("  FAIL  %s %s" % (name, detail))
            failures.append(name)

    # The GREEN arm: the shipped tree carries no finding.
    base = findings(root)
    check(
        "the shipped tree is GREEN — %d finding(s)" % len(base),
        not base,
        "; ".join(m for _, _, _, m in base),
    )

    text = read_text(os.path.join(root, "charly.yml"))

    # A: a surface that states the scope can be turned into one that does not.
    mutated = text.replace(
        "Advance EVERY pin this repo declares — this repo's own box/* gitlinks —",
        "Advance this repo's own box/* gitlinks —",
        1,
    )
    got = findings(root, {"charly.yml": mutated})
    check(
        "the SCOPE arm bites — a surface that drops the scope is a finding",
        any(k == "A" for k, _, _, _ in got),
        "(no A finding)",
    )

    # The EXISTENCE arm: a surface that MOVED is a finding, not a silent pass.
    moved = "\n".join(
        line
        for line in text.split("\n")
        if not line.lstrip().startswith("description: Advance EVERY pin this repo declares")
    )
    got = findings(root, {"charly.yml": moved})
    check(
        "the EXISTENCE arm bites — a surface whose line is gone is a finding",
        any(k == "B" and "GONE" in m for k, _, _, m in got),
        "(no GONE finding)",
    )

    # And the anchor is STRUCTURAL: the same surface, reworded, is still seen.
    reworded = text.replace(
        "description: Advance EVERY pin this repo declares — this repo's own box/* gitlinks —",
        "description: Advance EVERY pin this repo declares — this repo's own box/* links —",
        1,
    )
    got = findings(root, {"charly.yml": reworded})
    check(
        "the EXISTENCE arm is structural — a reworded surface is still located (no GONE "
        "finding)",
        not any(k == "B" and "GONE" in m for k, _, _, m in got),
        "(the reworded surface was reported as gone)",
    )

    # B: the wording this issue is ABOUT — the retired claim as it was shipped.
    original = text.replace(
        "description: Advance EVERY pin this repo declares — this repo's own box/* gitlinks — "
        "to the tip the remote serves for the ref each path's own .gitmodules entry names, "
        "never a PR branch; it rolls the checkouts that exist and stages their gitlinks, and "
        "it does not commit and does not open a PR",
        "description: Advance this repo's own box/* gitlinks to each distro's default-branch "
        "HEAD (preview only; does not commit)",
        1,
    )
    got = findings(root, {"charly.yml": original})
    check(
        "the RETIRED-CLAIM arm bites — the wording this issue is about ('preview only') is a "
        "finding",
        any(k == "B" and "non-mutating" in m for k, _, _, m in got),
        "(no retired-claim finding)",
    )

    # B: a retired claim in ANY tracked file is a finding.
    got = findings(root, {"README.md": "the pin advance is a preview\n"})
    check(
        "the RETIRED-CLAIM arm bites — a file that calls the advance a preview is a finding",
        any(k == "B" and "non-mutating" in m for k, _, _, m in got),
        "(no retired-claim finding)",
    )

    # C: a file that NAMES the verb and is not classified is a finding.
    got = findings(root, {"README.md": "run `charly task sync` before you push\n"})
    check(
        "the COVERAGE arm bites — a file that names the verb and is unclassified is a finding",
        any(k == "C" for k, _, _, m in got),
        "(no coverage finding)",
    )

    # And the classification cannot go stale.
    got = findings(
        root,
        {"README.md": "a README that says nothing about pins\n"},
        non_claim_mentions={"README.md": "a synthetic mention, for the arm's own test"},
    )
    check(
        "the STALE-CLASSIFICATION arm bites — a classified mention that no longer names the "
        "verb is a finding",
        any(k == "C" and "no longer names" in m for k, _, _, m in got),
        "(no stale-classification finding)",
    )

    # The SCOPE arm: a scope that stopped covering the claim-maker surfaces is a
    # finding, not a silent pass — the tracked set is emptied here, which is what
    # a rule file that exempts the wrong mode does.
    got = findings(root, tracked=[])
    check(
        "the SCOPE arm bites — a surface that is not a TRACKED file is a finding",
        any(k == "B" and "not a TRACKED file" in m for k, _, _, m in got),
        "(no scope finding)",
    )


    # D: the shared rule has ONE home — its grammar, its exempt-mode list, and the
    # readers that read it. Each mutation below is checked to have APPLIED before
    # its arm is credited: an unapplied mutation dies for free and proves nothing.
    rule_text = read_text(os.path.join(root, RULE_REL))
    reader_rel = "charly/retired_build_selector_test.go"
    reader_text = read_text(os.path.join(root, reader_rel))
    para = rule_paragraph(rule_text.split("\n"), GRAMMAR_PARAGRAPH_RE) if rule_text else None
    check(
        "the ONE-HOME arm has its fixtures — the rule file, its grammar paragraph and its "
        "named reader are all readable",
        rule_text is not None and reader_text is not None and para is not None,
        "(a fixture is unreadable, so the arm would prove nothing)",
    )

    if rule_text is not None and reader_text is not None and para is not None:
        # D: the shared rule has ONE home — its grammar, its exempt-mode list, and the
        # readers that read it. Each mutation below is checked to have APPLIED before
        # its arm is credited: an unapplied mutation dies for free and proves nothing.
        copied = reader_text + "\n// the grammar is one " + GRAMMAR_PHRASE + ", with `#` comments ignored\n"
        check(
            "the ONE-HOME arm bites — a reader that restates the GRAMMAR is a finding",
            copied != reader_text
            and GRAMMAR_RE.search(copied)
            and any(
                k == "D" and "GRAMMAR is restated here" in m for k, _, _, m in findings(root, {reader_rel: copied})
            ),
            "(the mutation did not apply, or no D finding)",
        )

        # The same copy INSIDE the rule file's own other paragraph is a finding: the
        # header is the grammar's one home, and the rule file's other paragraphs are
        # not it.
        lines = list(rule_text.split("\n"))
        # AFTER the paragraph's own separator: a copy that landed INSIDE the header
        # would be the header, and the mutation must not be the thing it claims to be.
        lines.insert(para[2] + 1, "# the grammar is one " + GRAMMAR_PHRASE + ", with `#` comments ignored")
        inside = "\n".join(lines)
        check(
            "the ONE-HOME arm bites — a copy of the GRAMMAR inside the rule file's own other "
            "paragraph is a finding",
            inside != rule_text
            and GRAMMAR_RE.search(inside)
            and any(
                k == "D" and "OUTSIDE its own header" in m for k, _, _, m in findings(root, {RULE_REL: inside})
            ),
            "(the mutation did not apply, or no D finding)",
        )

        # A rule file whose header stops stating the grammar is a finding: the one home
        # of the rule's syntax would be empty.
        without = GRAMMAR_RE.sub("", rule_text, count=1)
        check(
            "the ONE-HOME arm bites — a rule file whose header stops stating the GRAMMAR is a "
            "finding",
            without != rule_text
            and any(
                k == "D" and "does not state the GRAMMAR" in m for k, _, _, m in findings(root, {RULE_REL: without})
            ),
            "(the mutation did not apply, or no D finding)",
        )

        # A reader that copies the exempt-mode LIST is a finding: the list has one home.
        data_line = next(
            line for line in rule_text.split("\n") if line.strip() and not line.strip().startswith("#")
        )
        listed = reader_text + "\n// " + data_line.strip() + "\n"
        check(
            "the ONE-HOME arm bites — a reader that copies the exempt-mode LIST is a finding",
            listed != reader_text
            and any(
                k == "D" and "exempt-mode LIST is restated" in m for k, _, _, m in findings(root, {reader_rel: listed})
            ),
            "(the mutation did not apply, or no D finding)",
        )

        # A named reader whose code stops naming the rule file is a finding: it stopped
        # reading the rule, and it now parses a format the rule file no longer specifies.
        stopped = reader_text.replace('"tracked-files.txt"', '"rule-file.txt"')
        check(
            "the ONE-HOME arm bites — a named reader whose code stops naming the rule file is a "
            "finding",
            stopped != reader_text
            and QUOTED_RULE_RE.search(reader_text)
            and not QUOTED_RULE_RE.search(stopped)
            and any(
                k == "D" and "stopped reading the rule" in m for k, _, _, m in findings(root, {reader_rel: stopped})
            ),
            "(the mutation did not apply, or no D finding)",
        )

    if failures:
        print("check-pin-verb-scope --self-test: FAILED (%d arm(s))" % len(failures))
        return 1
    print(
        "check-pin-verb-scope --self-test: OK (%d arm(s): the shipped tree is green, and every "
        "arm goes red on its own mutation)" % len(arms)
    )
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=None, help="the tree to judge (default: this repo)")
    parser.add_argument("--self-test", action="store_true", help="prove every arm can fail")
    args = parser.parse_args()

    root = args.root or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

    if args.self_test:
        return self_test(root)

    try:
        found = findings(root)
    except GateError as exc:
        print("check-pin-verb-scope: %s — the claim was NOT judged" % exc, file=sys.stderr)
        return 1
    count = report(found)
    if count:
        print("check-pin-verb-scope: %d finding(s) in this repo's own tree" % count)
        return 1
    print(
        "check-pin-verb-scope: OK — %d claim-maker surface(s) state the pin-advance verb's "
        "scope, no tracked file calls the advance a non-mutating kind, and the shared rule "
        "has ONE home: its grammar and its exempt-mode list live in %s, and every reader it "
        "names reads it" % (len(SURFACES), RULE_REL)
    )
    print("PIN-VERB-SCOPE-OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
