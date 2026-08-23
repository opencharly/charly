# shellcheck shell=bash
# drift-baseline — compare a generator's CURRENT stale set against a checked-in baseline.
#
# drift-baseline — compare a generator's CURRENT stale set against a checked-in baseline.
#
# Single source of truth for baseline comparison. The skills:drift gate is its only remaining
# consumer since the docs de-submodule cutover: taskfiles/Docs.yml no longer has a `docs:drift`
# target (the docs repo's deploy workflow owns the docs content gate now — it regenerates on a
# fresh checkout at its pinned charly and fails on any diff, so the docs arm moved out of this
# repo entirely, baseline and all).
#   - taskfiles/Skills.yml `skills:drift` — `charly marketplace drift`'s artifact list
#
# BEFORE TRUSTING ANY CHECK HERE: ASK WHAT ITS OUTPUT SHAPE DISCARDS. Three instruments were
# each used in this repo to answer a question they cannot answer, and in every case the command
# succeeded and returned something that felt verified:
#
#   `git status` as a carrier test      answers "is this in sync", NOT "is this generated"
#   stat symmetry on a revert           answers "are the counts mirrored", NOT "is the content identical"
#   `gh ... --jq '.comments[] | .body'` CONCATENATES, so a line number from it indexes the
#                                       concatenation and no single comment has one — it cannot
#                                       tell you WHICH comment carried the text
#
# The instrument was fine each time; the question was not the one it answers. The durable forms:
# prove a revert by ABSENCE (`git diff main..head -- ':!intended' ':!files'` empty), prove a
# generated carrier by DELETING it and regenerating, and read a per-item artifact per item.
#
# THE SHARPEST MEMBER OF THAT FAMILY, because its WRONG answer is the default: an ancestry check
# run from the SUPERPROJECT against a SUBMODULE commit exits 128 — the objects are not in that
# repository, so the question was never evaluated. Verified here:
#
#	$ git -C plugins merge-base --is-ancestor <sha> origin/main ; echo $?   ->  0    (correct)
#	$ git          merge-base --is-ancestor <sha> origin/main ; echo $?   ->  128  (UNEVALUATED)
#
# 128 is falsy, so `if git merge-base --is-ancestor …; then` collapses "objects absent" and "not
# an ancestor" into the same branch and reports a pin as unmerged when it is merged. Run ancestry
# INSIDE the submodule, and test for rc=0 explicitly rather than relying on if/&&.
#
# WHAT A GREEN VERDICT ACTUALLY MEANS — read this before trusting one. The docs arm that
# used to feed this script moved with the docs de-submodule cutover: `docs:drift` read
# `git -C docs status --porcelain`, which compares the worktree against the CHECKED-OUT HEAD
# (on a CI checkout, the GITLINK) — so green meant "regeneration reproduces the gitlink", never
# "the site matches docs main". That trap no longer applies to docs: the docs repo's deploy
# workflow regenerates on a fresh checkout at its pinned charly and fails on any diff, and the
# pin itself is gated by `task docs:pin` against the live docs main head. The skills arm below
# still carries the same filesystem-vs-gitlink hazard — the preflight that makes it visible,
# worth running before acting on green:
#
#	git -C plugins rev-parse HEAD;  git ls-remote <plugins-remote> refs/heads/main
#
# HEAD != that repo's main means the verdict is about HEAD, not about main.
#
# WHERE THE REAL ASYMMETRY LIVES — in the GENERATORS, not here. `charly docs generate` does not
# read candy sources: collectSkills reads plugins/<plugin>/skills (candy/plugin-docs/gen_skills.go).
# The docs generator consumes the PROJECTION. So when a candy `skill:` entity has not landed, the
# chain breaks at hop 1 (candy -> plugins) and NOT at hop 2 (plugins -> docs) — docs re-emits the
# page from the copy sitting in plugins and the site stays clean, while a wholesale plugins
# regeneration deletes it as an orphan. A two-hop chain breaks at the hop nearest the missing
# source, not at every hop (CHANGELOG/2026.227.1725.md). That is why a wholesale docs projection
# is safe while a wholesale plugins projection can destroy an in-review page, and it is a fact
# about the generators that no amount of reading THIS file will tell you.
#
# WHY A BASELINE EXISTS AT ALL. Both generated trees are already stale on main, for
# changes nobody in a given PR made. An absolute gate would therefore red every PR in
# the repo until somebody else's work lands — and a gate that reds everything gets
# switched off, which leaves exactly the hole the gate was added to close. So the gate
# fails on NEW drift and merely REPORTS the debt it inherited.
#
# WHY THIS IS NOT AN ALLOWLIST. The comparison is EXACT MATCH, not superset: an entry
# that is listed but is no longer stale fails the gate just as loudly as an artifact
# that is stale but not listed. That is what makes the file self-liquidating. The
# moment the missing work lands, the gate reds with "no longer stale — delete these
# lines", the file shrinks, and when it reaches zero you delete the file itself and
# both gates are absolute again. A plain allowlist would instead sit there forever,
# quietly widening as people appended to it.
#
# A MISSING BASELINE FILE MEANS AN EMPTY BASELINE — i.e. the strict gate. Deleting the
# file is therefore the correct final action, not a way to disable anything.
#
# Usage:  <stale set on stdin, one path per line> | bash drift-baseline.sh <file> <label>

drift_baseline() {
	local baseline="$1" label="$2"
	local current expected new resolved rc=0 n

	if ! { current=$(mktemp) && expected=$(mktemp) && new=$(mktemp) && resolved=$(mktemp); }; then
		echo "drift-baseline: cannot create temp files" >&2
		return 2
	fi
	# shellcheck disable=SC2064
	trap "rm -f '$current' '$expected' '$new' '$resolved'" RETURN

	sed -e 's/[[:space:]]*$//' -e '/^$/d' | sort -u >"$current"

	# Comments and blank lines are stripped so the baseline file can explain itself.
	# An absent file yields an empty expected set — the strict gate.
	if [ -f "$baseline" ]; then
		sed -e 's/[[:space:]]*$//' -e 's/^[[:space:]]*//' -e '/^#/d' -e '/^$/d' "$baseline" | sort -u >"$expected"
	fi

	comm -23 "$current" "$expected" >"$new"      # stale now, not in the baseline
	comm -13 "$current" "$expected" >"$resolved" # in the baseline, no longer stale

	if [ -s "$new" ]; then
		echo "$label: DRIFT — $(wc -l <"$new") generated artifact(s) newly stale:" >&2
		sed 's/^/  /' "$new" >&2
		echo "Regenerate and commit the result — the generator is the source of truth." >&2
		rc=1
	fi
	if [ -s "$resolved" ]; then
		echo "$label: BASELINE STALE — $(wc -l <"$resolved") baseline entries are no longer stale:" >&2
		sed 's/^/  /' "$resolved" >&2
		echo "Delete those lines from $baseline — the debt they recorded is paid." >&2
		echo "With no entries left, delete the file itself; the gate then runs absolute." >&2
		rc=1
	fi
	if [ "$rc" -eq 0 ]; then
		n=$(wc -l <"$expected")
		if [ "$n" -eq 0 ]; then
			echo "$label: no drift."
		else
			echo "$label: no NEW drift. Carrying $n known-stale artifact(s) recorded in $baseline:"
			sed 's/^/  /' "$expected"
		fi
	fi
	return "$rc"
}

# Direct execution is the only supported entry point — the Taskfiles pipe into it.
if [ "${BASH_SOURCE[0]:-$0}" = "$0" ]; then
	if [ "$#" -ne 2 ]; then
		echo "usage: <stale set on stdin> | bash drift-baseline.sh <baseline-file> <label>" >&2
		exit 2
	fi
	drift_baseline "$1" "$2"
fi
