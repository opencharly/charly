# shellcheck shell=bash
# drift-baseline — compare a generator's CURRENT stale set against a checked-in baseline.
#
# Single source of truth for baseline comparison, shared (R3) by both drift gates:
#   - taskfiles/Skills.yml `skills:drift` — `charly marketplace drift`'s artifact list
#   - taskfiles/Docs.yml   `docs:drift`   — `git -C docs status --porcelain`'s path list
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
