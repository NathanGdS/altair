#!/usr/bin/env bash
# Fires an autocannon load at a resource-limited altair container with an example consumer
# (EXAMPLE, default 01-consumer) registered for the load's origin, so every publish also flows
# through to webhook deliveries. Prints the autocannon result, the consumer's received count and
# the containers' resource usage; it does not wait for the deliveries to drain or judge them (that
# is validate.sh). Tunables (env): EXAMPLE, CONNECTIONS, DURATION, PIPELINING, REQUEST
# (smoke|heavy), ORIGIN, any perf.env key, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS, LOADER_MEM,
# KEEP=1 to leave the stack running afterwards.
#
# INTERACTIVE=1 runs in three phases so the altair UI and the consumer's /stats can be watched
# (published on 127.0.0.1:ALTAIR_UI_PORT / CONSUMER_STATS_PORT, defaults 8080 / 9090): 1) altair
# and consumer up, wait for Enter; 2) fire the load, then Enter fires another round on the same
# stack or q finishes; 3) clean up.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

example="${EXAMPLE:-01-consumer}"
require_example "$example"
export EXAMPLE="$example"
load_perf_env "../examples/$example/perf.env"
export ORIGIN="${ORIGIN:-stress-test}"

connections="${CONNECTIONS:-20}"
duration="${DURATION:-60}"
pipelining="${PIPELINING:-5}"
request="${REQUEST:-heavy}"
interactive="${INTERACTIVE:-0}"
case "$request" in smoke|heavy) ;; *) die "REQUEST must be smoke|heavy, got '$request'" ;; esac
[ "$interactive" != "1" ] || require_interactive_ports

fire_load() {
  echo "Firing load: -c $connections -d $duration -p $pipelining ($request payload, origin $ORIGIN)..."
  # </dev/null: `compose run` forwards stdin, which would swallow the interactive Enter/q.
  compose run --rm -T loader -c "$connections" -d "$duration" -p "$pipelining" \
    -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" \
    http://altair:8080/publish </dev/null

  echo "consumer received so far: $(consumer_received) (deliveries keep flowing after the load ends)"
  echo "Resource usage right after the load:"
  docker stats --no-stream $(compose ps -q altair consumer)
}

trap perf_cleanup EXIT

echo "Building images and starting altair (cpus=${ALTAIR_CPUS:-2}, mem=${ALTAIR_MEM:-4g}) and consumer $example (cpus=${CONSUMER_CPUS:-1}, mem=${CONSUMER_MEM:-512m})..."
render_requests "$ORIGIN"
perf_setup altair consumer loader
compose up -d --wait altair
compose up -d consumer
wait_for_consumer

if [ "$interactive" != "1" ]; then
  fire_load
  exit 0
fi

echo "[1/3] altair and consumer are up."
print_ui_urls with-consumer
prompt_continue "Press Enter to fire the load (q to finish and clean up)... " || exit 0

round=1
while true; do
  echo "[2/3] Round $round"
  fire_load
  print_ui_urls with-consumer
  prompt_continue "Enter = fire another round, q = finish and clean up: " || break
  round=$((round + 1))
done
echo "[3/3] Finishing after $round round(s)."
