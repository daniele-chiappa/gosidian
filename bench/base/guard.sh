#!/bin/sh
# guard.sh IMAGE OUT [FLAGS...] — the base's guard (the roadmap's M1.4):
# waits for the host's load to go under 4, at most 15 minutes, measures
# IMAGE with three runs into OUT, and checks it against
# bench/base/reference.json. A check that fails is measured again, and the
# guard exits 1 when the second fails too: the nightly run's stage base,
# and the step before a tag. FLAGS go to `base run` (e.g. -repo and -ref,
# for the code at the version).
#
#   bench/base/guard.sh gitea.example.com/org/gosidian:main-1a23ee5 /tmp/guard.json
set -eu

image=${1:?usage: guard.sh IMAGE OUT [FLAGS...]}
out=${2:?usage: guard.sh IMAGE OUT [FLAGS...]}
shift 2
here=$(cd "$(dirname "$0")" && pwd)
cache=${BENCH_BASE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/gosidian-bench-base}
max_load=${GUARD_MAX_LOAD:-4}
max_wait=${GUARD_MAX_WAIT:-900}

# The times mean little above a load of 4 (M1.2), and the stages before
# the nightly's leave the host busy for a while.
quiet() {
	waited=0
	while ! awk -v m="$max_load" '{ exit !($1 < m) }' /proc/loadavg && [ "$waited" -lt "$max_wait" ]; do
		sleep 30
		waited=$((waited + 30))
	done
	if [ "$waited" -gt 0 ]; then
		echo "guard: waited ${waited}s for the load to go under $max_load; it is $(cut -d' ' -f1 /proc/loadavg)"
	fi
}

# set -e does not hold in an if's condition: a bench that fails returns.
measure() {
	quiet
	rm -f "$out"
	sh "$here/run.sh" "$image" "${GUARD_LABEL:-guard}" 3 -out "$out" "$@" >/dev/null || return 1
	"$cache/base" check -ref "$here/reference.json" "$out"
}

# Every number is the median of the three runs, so one run the host slows
# does not count (on 2026-10-29 the first of three was twice as slow in
# everything, its CPU per call too). A host busy for longer still can: a
# number past its limit counts when a second measure finds it again.
if ! measure "$@"; then
	echo
	echo "guard: past the limits; measuring again"
	measure "$@"
fi
