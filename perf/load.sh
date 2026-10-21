#!/usr/bin/env bash
# Fires an autocannon load at a resource-limited altair container and prints the result plus
# the container's resource usage. No consumer is involved. Tunables (env): CONNECTIONS,
# DURATION, PIPELINING, REQUEST (smoke|heavy), ORIGIN, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS,
# LOADER_MEM, KEEP=1 to leave the stack running afterwards.
#
# INTERACTIVE=1 runs in three phases so the altair UI can be watched (published on
# 127.0.0.1:ALTAIR_UI_PORT, default 8080): 1) altair up, wait for Enter; 2) fire the load, then
# Enter fires another round on the same stack or q finishes; 3) clean up.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

connections="${CONNECTIONS:-20}"
duration="${DURATION:-60}"
pipelining="${PIPELINING:-5}"
request="${REQUEST:-heavy}"
interactive="${INTERACTIVE:-0}"
case "$request" in smoke|heavy) ;; *) die "REQUEST must be smoke|heavy, got '$request'" ;; esac
[ "$interactive" != "1" ] || require_interactive_ports

fire_load() {
  echo "Firing load: -c $connections -d $duration -p $pipelining ($request payload)..."
  # </dev/null: `compose run` forwards stdin, which would swallow the interactive Enter/q.
  compose run --rm -T loader -c "$connections" -d "$duration" -p "$pipelining" \
    -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" \
    http://altair:8080/publish </dev/null

  echo "altair resource usage right after the load:"
  docker stats --no-stream "$(compose ps -q altair)"
}

trap perf_cleanup EXIT

echo "Building images and starting altair (cpus=${ALTAIR_CPUS:-2}, mem=${ALTAIR_MEM:-4g})..."
render_requests "${ORIGIN:-stress-test}"
perf_setup altair loader
compose up -d --wait altair

if [ "$interactive" != "1" ]; then
  fire_load
  exit 0
fi

echo "[1/3] altair is up."
print_ui_urls
prompt_continue "Press Enter to fire the load (q to finish and clean up)... " || exit 0

round=1
while true; do
  echo "[2/3] Round $round"
  fire_load
  print_ui_urls
  prompt_continue "Enter = fire another round, q = finish and clean up: " || break
  round=$((round + 1))
done
echo "[3/3] Finishing after $round round(s)."
