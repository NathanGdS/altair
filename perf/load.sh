#!/usr/bin/env bash
# Fires an autocannon load at a resource-limited altair container and prints the result plus
# the container's resource usage. No consumer is involved. Tunables (env): CONNECTIONS,
# DURATION, PIPELINING, REQUEST (smoke|heavy), ORIGIN, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS,
# LOADER_MEM, KEEP=1 to leave the stack running afterwards.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

connections="${CONNECTIONS:-20}"
duration="${DURATION:-5}"
pipelining="${PIPELINING:-5}"
request="${REQUEST:-heavy}"
case "$request" in smoke|heavy) ;; *) die "REQUEST must be smoke|heavy, got '$request'" ;; esac

trap perf_cleanup EXIT

echo "Building images and starting altair (cpus=${ALTAIR_CPUS:-2}, mem=${ALTAIR_MEM:-4g})..."
render_requests "${ORIGIN:-stress-test}"
perf_setup altair loader
compose up -d --wait altair

echo "Firing load: -c $connections -d $duration -p $pipelining ($request payload)..."
compose run --rm -T loader -c "$connections" -d "$duration" -p "$pipelining" \
  -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" \
  http://altair:8080/publish

echo "altair resource usage right after the load:"
docker stats --no-stream "$(compose ps -q altair)"
