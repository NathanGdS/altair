#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$script_dir/../.." && pwd)"
cd "$root"

request_count=500

echo "Building altair and example consumer..."
go build -o bin/altair ./main.go
go build -o bin/example-consumer ./examples/01-consumer

echo "Ensuring runtime directories exist (avoids startup race in workers.DeleteMakedFiles)..."
mkdir -p messages/ready messages/processed messages/trash data deliveries/pending deliveries/failed

echo "Starting altair..."
./bin/altair &
server_pid=$!
sleep 2

echo "Starting example consumer..."
./bin/example-consumer &
consumer_pid=$!
sleep 2

cleanup() {
  kill "$server_pid" "$consumer_pid" 2>/dev/null || true
}
trap cleanup EXIT

echo "Firing $request_count requests via autocannon..."
npx autocannon -c 10 -a "$request_count" -m POST -H "Content-Type: application/json" -i "$script_dir/request.json" http://localhost:8080/publish

echo "Waiting for deliveries to drain..."
deadline=$((SECONDS + 30))
received=0
while [ $SECONDS -lt $deadline ]; do
  sleep 2
  received=$(curl -s http://localhost:9090/stats | grep -o '"received":[0-9]*' | grep -o '[0-9]*$' || echo 0)
  if [ "$received" -ge "$request_count" ]; then
    break
  fi
done

if [ "$received" -eq "$request_count" ]; then
  echo "PASS: consumer received $received/$request_count deliveries"
  exit 0
else
  echo "FAIL: consumer received $received/$request_count deliveries"
  exit 1
fi
