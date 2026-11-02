#!/bin/sh
# run.sh IMAGE FORM [LABEL] [RUNS] [FLAGS...]: level A on the backlog of
# bench/backlog (IMP-212, phase C). It builds the generator and the level A
# harness in the Go image, writes the two vaults, starts IMAGE on the FORM
# one (sections or database) in a throwaway container (2 CPUs, 2 GB, port
# on the loopback) with a read token, and runs the gosidian configuration
# on bench/backlog/questions.jsonl, its results labelled LABEL (default:
# the image's tag and the form) and appended to
# bench/results/agents-<date>-backlog.jsonl. FLAGS go to the harness
# (-ids, -out…).
# The sessions run on this machine's claude and use model quota.
#
#   sh bench/backlog/run.sh gosidian:v2.119.0 database
#   BENCH_BACKLOG_PORT=8085 sh bench/backlog/run.sh gosidian:v2.42.0 sections v2.42.0-sections 2
set -eu
image=${1:?usage: run.sh IMAGE FORM [LABEL] [RUNS] [FLAGS...]}
form=${2:?usage: run.sh IMAGE FORM [LABEL] [RUNS] [FLAGS...]}
label=${3:-${image##*:}-$form}
runs=${4:-1}
shift $(($# < 4 ? $# : 4))
case $form in sections | database) ;; *) echo "run.sh: FORM is sections or database" >&2 && exit 2 ;; esac
port=${BENCH_BACKLOG_PORT:-8083}
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=${BENCH_BACKLOG_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/gosidian-bench-backlog}/$port
name=gos-backlog-$port

mkdir -p "$work"
docker run --rm --cpus=8 -v "$repo":/src -v "$work":/out \
	-v gosidian-gomod:/go/pkg/mod -v gosidian-gocache:/root/.cache/go-build \
	-w /src -e CGO_ENABLED=0 golang:1.27-alpine \
	sh -c "go build -o /out/agents ./bench/agents && go build -o /out/backlog ./bench/backlog &&
		rm -rf /out/vault && /out/backlog -out /out/vault && chown -R $(id -u):$(id -g) /out"

cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	docker volume rm "$name-vault" "$name-state" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM
cleanup
docker volume create "$name-vault" >/dev/null
docker run --rm --cpus=1 -v "$name-vault":/v -v "$work/vault/$form":/src:ro alpine:3.20 \
	sh -c 'cp -a /src/. /v/ && chown -R 65532:65532 /v'
docker run -d --name "$name" --cpus=2 --memory=2g -p "127.0.0.1:$port:8080" \
	-e GOSIDIAN_STATE_DIR=/data -v "$name-vault":/vault -v "$name-state":/data "$image" >/dev/null
i=0
until [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/healthz")" = 200 ]; do
	i=$((i + 1)) && [ $i -lt 300 ] || { echo "run.sh: $image is not healthy" >&2 && exit 1; }
	sleep 0.2
done
# The CLI prints the token twice (the token and the client command): the
# first is it. It stays in this shell and the harness's environment.
token=$(docker exec "$name" /gosidian token create --vault /vault --state-dir /data \
	--name bench --scopes read | grep -oE 'gosidian_[A-Za-z0-9_-]+' | head -n 1)
[ -n "$token" ] || { echo "run.sh: no token" >&2 && exit 1; }

cd "$repo"
GOSIDIAN_BENCH_TOKEN=$token "$work/agents" -config gosidian -label "$label" -runs "$runs" \
	-mcp-url "http://127.0.0.1:$port/mcp" -questions bench/backlog/questions.jsonl \
	-project harbor -projects harbor -out "bench/results/agents-$(date -u +%F)-backlog.jsonl" "$@"
