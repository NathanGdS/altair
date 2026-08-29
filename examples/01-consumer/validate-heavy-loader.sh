#!/usr/bin/env bash
# Heavy end-to-end validation: builds altair + the example consumer, fires the same
# heavy autocannon load as the project root's loader.sh (duration-based, not a fixed
# request count) at /publish, then confirms the consumer's webhook received exactly as
# many deliveries as altair actually accepted (2xx responses).
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$script_dir/../.." && pwd)"
cd "$root"

# Same shape as the root's request-loader.json: "stress-test": true, no explicit origin,
# so the server defaults origin to "stress-test" (handlers/publish_handler.go). The example
# consumer below is registered against that same origin.
request_file="$root/request-loader.json"
origin="stress-test"
server_pid=""
consumer_pid=""

cleanup() {
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  [ -n "$consumer_pid" ] && kill "$consumer_pid" 2>/dev/null || true
}
trap cleanup EXIT

echo "Building altair and example consumer..."
go build -o bin/altair ./main.go
go build -o bin/example-consumer ./examples/01-consumer

echo "Resetting runtime state for a clean heavy-load run..."
mkdir -p messages/ready messages/processed messages/trash data deliveries/pending deliveries/failed
find messages/ready messages/processed messages/trash deliveries/pending deliveries/failed -type f -delete
rm -f data/altair.db

echo "Starting altair..."
./bin/altair &
server_pid=$!
sleep 2

echo "Starting example consumer (origin: $origin)..."
./bin/example-consumer -origin "$origin" &
consumer_pid=$!
sleep 2

echo "Firing heavy load via autocannon (same profile as loader.sh: -c 20 -d 5 -p 5)..."
result_json=$(npx autocannon -c 20 -d 5 -p 5 -m POST -H "Content-Type: application/json" -i "$request_file" --json http://localhost:8080/publish)

accepted=$(echo "$result_json" | grep -o '"2xx":[0-9]*' | grep -o '[0-9]*$' || echo 0)
sent=$(echo "$result_json" | grep -o '"sent":[0-9]*' | grep -o '[0-9]*$' || echo 0)
echo "autocannon: $accepted 2xx responses, $sent requests sent"

if [ "$accepted" -le 0 ]; then
  echo "FAIL: autocannon reported 0 successful (2xx) publishes"
  exit 1
fi

echo "Waiting for deliveries to drain (this scales with the load above; progress logged every ~10s)..."
# A fixed wall-clock deadline doesn't work at this scale: a heavy enough run can produce far
# more accepted publishes than the bounded delivery pool can drain in any short window.
# Instead, keep polling as long as `received` is still climbing, and only give up once it
# stalls (no progress for idle_timeout seconds) or a generous absolute ceiling is hit.
idle_timeout=30
absolute_max=1800
started=$SECONDS
received=0
last_received=-1
last_progress=$SECONDS
last_log=$SECONDS
while true; do
  sleep 2
  received=$(curl -s http://localhost:9090/stats | grep -o '"received":[0-9]*' | grep -o '[0-9]*$' || echo 0)

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

failed_count=$(find deliveries/failed -type f 2>/dev/null | wc -l | tr -d ' ')

# Delivery is at-least-once by design (design spec: a webhook timeout triggers a retry even
# if the original attempt eventually succeeds server-side), so `received` may exceed
# `accepted` under heavy load without that being a bug -- especially here, where altair, the
# example consumer, and autocannon are all fighting for CPU on one machine, which can push
# the 5s DeliveryHTTPTimeout even for a webhook handler that's instant on its own. The only
# real failure is `received` falling short (lost messages) or a non-empty deliveries/failed
# (a delivery that exhausted retries and was never recovered).
if [ "$received" -ge "$accepted" ] && [ "$failed_count" -eq 0 ]; then
  extra=$((received - accepted))
  if [ "$extra" -gt 0 ]; then
    echo "PASS: consumer received $received/$accepted deliveries ($extra likely retry-on-timeout duplicates, deliveries/failed empty)"
  else
    echo "PASS: consumer received $received/$accepted deliveries, deliveries/failed empty"
  fi
  exit 0
else
  echo "FAIL: consumer received $received/$accepted deliveries, deliveries/failed has $failed_count file(s)"
  exit 1
fi
