#!/bin/sh
# run.sh IMAGE [LABEL] [RUNS] [FLAGS...] — measures one gosidian image with
# bench/base; FLAGS go to `base run` (e.g. -env GOSIDIAN_VAULT_DURABILITY=none).
#
# Builds bench/base and bench/scale in a Go container (the host needs only
# docker and git), makes the large vault once (5000 noise notes, seed 7,
# kept in $BENCH_BASE_CACHE), and runs the measures from the repository
# root. Results go to bench/results/base-<date>-<label>.json.
#
#   bench/base/run.sh gitea.example.com/org/gosidian:v2.119.0
#   bench/base/run.sh gosidian:lab lab 1
set -eu

image=${1:?usage: run.sh IMAGE [LABEL] [RUNS] [FLAGS...]}
label=${2:-}
runs=${3:-3}
shift $(($# < 3 ? $# : 3))
repo=$(cd "$(dirname "$0")/../.." && pwd)
cache=${BENCH_BASE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/gosidian-bench-base}
large_notes=${BENCH_BASE_LARGE_NOTES:-5000}
mkdir -p "$cache"

# The binaries, static, owned by the caller.
docker run --rm --cpus=8 -v "$repo":/src -v "$cache":/out \
	-v gosidian-gomod:/go/pkg/mod -v gosidian-gocache:/root/.cache/go-build \
	-w /src -e CGO_ENABLED=0 golang:1.27-alpine \
	sh -c "go build -buildvcs=false -o /out/base ./bench/base && go build -buildvcs=false -o /out/scale ./bench/scale && chown $(id -u):$(id -g) /out/base /out/scale"

# The large vault, made once per size: bench/scale is deterministic for a seed.
large="$cache/vault-$large_notes"
if [ ! -d "$large" ]; then
	(cd "$repo" && "$cache/scale" -base bench/vault -out "$large" -notes "$large_notes" -seed 7)
fi

cd "$repo"
exec "$cache/base" run -image "$image" ${label:+-label "$label"} -runs "$runs" -large "$large" "$@"
