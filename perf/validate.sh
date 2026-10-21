#!/usr/bin/env bash
# End-to-end delivery validation inside the resource-limited Docker stack: publishes to altair,
# then checks what the example consumer's webhook received against that example's perf.env
# expectations (see examples/README.md).
#
# Usage: ./perf/validate.sh [smoke|heavy] [<example>|all]     (defaults: smoke 01-consumer)
#   smoke: fixed 500 requests.
#   heavy: duration-based load (CONNECTIONS/DURATION/PIPELINING, default -c 20 -d 5 -p 5).
#   all:   every examples/*/ with a Dockerfile, each on a fresh stack, then a PASS/FAIL summary.
# Env (overrides perf.env): ORIGIN, EXPECT, EXPECT_FAILED, DRAIN_IDLE_TIMEOUT, CONSUMER_CPUS,
# CONSUMER_MEM, CONSUMER_ARGS. Other env: ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS, LOADER_MEM, KEEP=1.
#
# INTERACTIVE=1 (single example only) runs in three phases so the altair UI and the consumer's
# /stats can be watched (published on 127.0.0.1:ALTAIR_UI_PORT / CONSUMER_STATS_PORT, defaults
# 8080 / 9090): 1) stack up, wait for Enter; 2) fire + validate a round, then Enter fires another
# round on the same stack or q finishes; 3) clean up. Rounds are checked against cumulative
# totals, since the consumer's counter and deliveries/failed accumulate across rounds.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

mode="${1:-smoke}"
target="${2:-01-consumer}"
interactive="${INTERACTIVE:-0}"
smoke_count=500

case "$mode" in
  smoke|heavy) ;;
  *) echo "usage: $0 [smoke|heavy] [<example>|all]" >&2; exit 2 ;;
esac

if [ "$target" = "all" ]; then
  mapfile -t examples < <(list_examples)
  [ "${#examples[@]}" -gt 0 ] || die "no examples/*/Dockerfile found"
  [ "${KEEP:-0}" != "1" ] || die "KEEP=1 is not supported with 'all' (each example replaces the previous stack)"
  [ "$interactive" != "1" ] || die "INTERACTIVE=1 is not supported with 'all' (each example replaces the previous stack)"
else
  require_example "$target"
  examples=("$target")
fi
[ "$interactive" != "1" ] || require_interactive_ports

# Fires one round and checks the cumulative totals. Sets round_ok=1/0 instead of returning a
# status: a status checked by the caller would disable errexit inside this function.
run_round() {
  local round="$1"
  round_ok=0

  echo "Firing $mode load via autocannon (round $round)..."
  local result_json accepted sent
  # </dev/null: `compose run` forwards stdin, which would swallow the interactive Enter/q.
  result_json=$(compose run --rm -T loader "${load_args[@]}" --json \
    -m POST -H "Content-Type: application/json" -i "/requests/request-$mode.json" \
    http://altair:8080/publish </dev/null)
  accepted=$(echo "$result_json" | grep -o '"2xx":[0-9]*' | grep -o '[0-9]*$' || echo 0)
  sent=$(echo "$result_json" | grep -o '"sent":[0-9]*' | grep -o '[0-9]*$' || echo 0)
  echo "autocannon: $accepted 2xx responses, $sent requests sent"

  if [ "$accepted" -le 0 ]; then
    echo "FAIL: autocannon reported 0 successful (2xx) publishes"
    return 0
  fi
  if [ "$mode" = "smoke" ] && [ "$accepted" -ne "$smoke_count" ]; then
    echo "FAIL: altair accepted $accepted/$smoke_count smoke publishes"
    return 0
  fi
  total_accepted=$((total_accepted + accepted))

  echo "Waiting for deliveries to drain (progress logged every ~10s)..."
  # Keep polling while `received` is still climbing; give up only once it stalls for
  # idle_timeout seconds or the absolute ceiling is hit (a heavy run can take minutes to drain).
  local absolute_max=1800 started=$SECONDS received=0 last_received=-1
  local last_progress=$SECONDS last_log=$SECONDS
  while true; do
    sleep 2
    received=$(consumer_received)
    received="${received:-0}"

    if [ "$received" -ne "$last_received" ]; then
      last_received=$received
      last_progress=$SECONDS
    fi
    if [ "$received" -ge "$total_accepted" ]; then
      break
    fi

    if [ $((SECONDS - last_log)) -ge 10 ]; then
      echo "  ... $received/$total_accepted delivered so far"
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

  local failed_count
  failed_count=$(compose exec -T altair find /app/deliveries/failed -type f </dev/null | wc -l | tr -d ' ')

  echo "Resource usage at the end of the round:"
  docker stats --no-stream $(compose ps -q altair consumer)

  # Delivery is at-least-once (a webhook timeout triggers a retry even if the original attempt
  # eventually succeeded), so `received` may exceed `accepted` without that being a bug; that is
  # why heavy runs default to EXPECT=at-least.
  local problems=()
  case "$expect" in
    exact)    [ "$received" -eq "$total_accepted" ] || problems+=("received $received, expected exactly $total_accepted") ;;
    at-least) [ "$received" -ge "$total_accepted" ] || problems+=("received $received, expected at least $total_accepted") ;;
  esac
  case "$expect_failed" in
    empty)     [ "$failed_count" -eq 0 ] || problems+=("deliveries/failed has $failed_count file(s), expected empty") ;;
    non-empty) [ "$failed_count" -gt 0 ] || problems+=("deliveries/failed empty, expected non-empty") ;;
  esac

  local summary="received $received/$total_accepted ($expect), deliveries/failed $failed_count file(s) ($expect_failed)"
  if [ "${#problems[@]}" -eq 0 ]; then
    echo "PASS: $summary"
    round_ok=1
    return 0
  fi
  echo "FAIL: $summary"
  printf '  - %s\n' "${problems[@]}"
}

# Validates one example and exits with its result. Always called in a subshell so the exports
# from its perf.env never leak into the next example.
validate_example() {
  local example="$1"
  export EXAMPLE="$example"
  load_perf_env "../examples/$example/perf.env"

  local origin="${ORIGIN:-stress-test}"
  expect="${EXPECT:-auto}"
  expect_failed="${EXPECT_FAILED:-empty}"
  idle_timeout="${DRAIN_IDLE_TIMEOUT:-30}"
  export ORIGIN="$origin"

  case "$expect" in auto|exact|at-least|none) ;; *) die "EXPECT must be auto|exact|at-least|none, got '$expect'" ;; esac
  case "$expect_failed" in empty|non-empty|any) ;; *) die "EXPECT_FAILED must be empty|non-empty|any, got '$expect_failed'" ;; esac
  [[ "$idle_timeout" =~ ^[0-9]+$ ]] || die "DRAIN_IDLE_TIMEOUT must be a number of seconds, got '$idle_timeout'"
  if [ "$expect" = "auto" ]; then
    if [ "$mode" = "smoke" ]; then expect="exact"; else expect="at-least"; fi
  fi

  if [ "$mode" = "smoke" ]; then
    load_args=(-c 10 -a "$smoke_count")
  else
    load_args=(-c "${CONNECTIONS:-20}" -d "${DURATION:-5}" -p "${PIPELINING:-5}")
  fi

  echo "[$example] $mode: ORIGIN=$origin EXPECT=$expect EXPECT_FAILED=$expect_failed (altair cpus=${ALTAIR_CPUS:-2}, mem=${ALTAIR_MEM:-4g}; consumer cpus=${CONSUMER_CPUS:-1}, mem=${CONSUMER_MEM:-512m})"
  render_requests "$origin"
  perf_setup
  compose up -d --wait altair
  compose up -d consumer
  wait_for_consumer

  total_accepted=0
  local round=1 failed_rounds=0

  if [ "$interactive" = "1" ]; then
    echo "[1/3] altair and consumer are up."
    print_ui_urls with-consumer
    if ! prompt_continue "Press Enter to fire the load (q to finish and clean up)... "; then
      echo "No load fired."
      exit 0
    fi
  fi

  while true; do
    [ "$interactive" != "1" ] || echo "[2/3] Round $round"
    run_round "$round"
    [ "$round_ok" = "1" ] || failed_rounds=$((failed_rounds + 1))

    [ "$interactive" = "1" ] || break
    print_ui_urls with-consumer
    prompt_continue "Enter = fire another round, q = finish and clean up: " || break
    round=$((round + 1))
  done

  if [ "$interactive" = "1" ]; then
    echo "[3/3] Finishing after $round round(s), $failed_rounds failed."
  fi
  [ "$failed_rounds" -eq 0 ] || exit 1
  exit 0
}

trap perf_cleanup EXIT

results=()
failures=0
for example in "${examples[@]}"; do
  # Not `if ( ... )`: bash ignores errexit inside a condition, so a failing docker command
  # would not abort the example. errexit is re-enabled inside the subshell instead.
  set +e
  (
    set -e
    trap '[ "${KEEP:-0}" = "1" ] || perf_teardown_stack' EXIT
    validate_example "$example"
  )
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    results+=("$(printf '%-24s PASS' "$example")")
  else
    results+=("$(printf '%-24s FAIL' "$example")")
    failures=$((failures + 1))
  fi
done

if [ "$target" = "all" ]; then
  echo
  echo "Summary ($mode):"
  printf '  %s\n' "${results[@]}"
fi
[ "$failures" -eq 0 ]
