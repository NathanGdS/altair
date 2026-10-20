#!/usr/bin/env bash
# End-to-end delivery validation inside the resource-limited Docker stack: publishes to altair,
# then confirms the example consumer's webhook received every accepted (2xx) publish and that
# deliveries/failed stayed empty.
#
# Modes:
#   smoke (default): fixed 500 requests, consumer must receive exactly that many.
#   heavy:           duration-based load (CONNECTIONS/DURATION/PIPELINING, default -c 20 -d 5 -p 5);
#                    received may exceed accepted (at-least-once retries), never fall short.
# Other env: ALTAIR_CPUS, ALTAIR_MEM, CONSUMER_CPUS, CONSUMER_MEM, LOADER_CPUS, LOADER_MEM, KEEP=1.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

mode="${1:-smoke}"
case "$mode" in
  smoke)
    smoke_count=500
    load_args=(-c 10 -a "$smoke_count" -i /requests/request-smoke.json) ;;
  heavy)
    load_args=(-c "${CONNECTIONS:-20}" -d "${DURATION:-5}" -p "${PIPELINING:-5}" -i /requests/request-heavy.json) ;;
  *)
    echo "usage: $0 [smoke|heavy]" >&2; exit 2 ;;
esac

trap perf_cleanup EXIT

consumer_received() {
  compose exec -T consumer wget -qO- http://localhost:9090/stats 2>/dev/null \
    | grep -o '"received":[0-9]*' | grep -o '[0-9]*$' || echo 0
}

echo "Building images and starting a clean stack (altair cpus=${ALTAIR_CPUS:-2}, mem=${ALTAIR_MEM:-4g})..."
perf_setup
compose up -d --wait altair consumer

echo "Firing $mode load via autocannon..."
result_json=$(compose run --rm -T loader "${load_args[@]}" --json \
  -m POST -H "Content-Type: application/json" http://altair:8080/publish)

accepted=$(echo "$result_json" | grep -o '"2xx":[0-9]*' | grep -o '[0-9]*$' || echo 0)
sent=$(echo "$result_json" | grep -o '"sent":[0-9]*' | grep -o '[0-9]*$' || echo 0)
echo "autocannon: $accepted 2xx responses, $sent requests sent"

if [ "$accepted" -le 0 ]; then
  echo "FAIL: autocannon reported 0 successful (2xx) publishes"
  exit 1
fi

echo "Waiting for deliveries to drain (progress logged every ~10s)..."
# Keep polling while `received` is still climbing; give up only once it stalls for
# idle_timeout seconds or the absolute ceiling is hit (a heavy run can take minutes to drain).
idle_timeout=30
absolute_max=1800
started=$SECONDS
received=0
last_received=-1
last_progress=$SECONDS
last_log=$SECONDS
while true; do
  sleep 2
  received=$(consumer_received)

  if [ "$received" -ne "$last_received" ]; then
    last_received=$received
    last_progress=$SECONDS
  fi
  if [ "$received" -ge "$accepted" ]; then
    break
  fi

  if [ $((SECONDS - last_log)) -ge 10 ]; then
    echo "  ... $received/$accepted delivered so far"
    last_log=$SECONDS
  fi
  if [ $((SECONDS - last_progress)) -ge "$idle_timeout" ]; then
    echo "  stalled: no new deliveries for ${idle_timeout}s"
    break
  fi
  if [ $((SECONDS - started)) -ge "$absolute_max" ]; then
    echo "  gave up after ${absolute_max}s"
    break
  fi
done

failed_count=$(compose exec -T altair find /app/deliveries/failed -type f | wc -l | tr -d ' ')

echo "Resource usage at the end of the run:"
docker stats --no-stream $(compose ps -q altair consumer)

if [ "$mode" = "smoke" ]; then
  if [ "$accepted" -eq "$smoke_count" ] && [ "$received" -eq "$accepted" ] && [ "$failed_count" -eq 0 ]; then
    echo "PASS: consumer received $received/$smoke_count deliveries, deliveries/failed empty"
    exit 0
  fi
  echo "FAIL: consumer received $received/$smoke_count deliveries ($accepted accepted), deliveries/failed has $failed_count file(s)"
  exit 1
fi

# Delivery is at-least-once (a webhook timeout triggers a retry even if the original attempt
# eventually succeeded), so `received` may exceed `accepted` under heavy load without that
# being a bug. The only real failures are lost messages or a non-empty deliveries/failed.
if [ "$received" -ge "$accepted" ] && [ "$failed_count" -eq 0 ]; then
  extra=$((received - accepted))
  if [ "$extra" -gt 0 ]; then
    echo "PASS: consumer received $received/$accepted deliveries ($extra likely retry-on-timeout duplicates, deliveries/failed empty)"
  else
    echo "PASS: consumer received $received/$accepted deliveries, deliveries/failed empty"
  fi
  exit 0
fi
echo "FAIL: consumer received $received/$accepted deliveries, deliveries/failed has $failed_count file(s)"
exit 1
