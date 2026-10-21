# Shared helpers for the perf scripts (sourced, not executed; cwd must be perf/). Every Docker
# resource a run creates is scoped so perf_cleanup can remove all of it without touching anything
# else on the machine: containers/network/volumes via the compose project, images by their
# `:perf` tag, and build cache + base images inside a dedicated buildx builder deleted as a whole.

export MSYS_NO_PATHCONV=1 # Git Bash: keep container paths like /requests/... untouched

perf_builder="altair-perf"
buildkit_image="moby/buildkit:buildx-stable-1"
# Marks that the buildkit image was pulled by us (not present before), so cleanup may remove it
# even when the run that pulled it was interrupted.
buildkit_marker=".buildkit-pulled"
run_dir=".run"

# Keys an examples/<name>/perf.env may set (see examples/README.md).
perf_env_keys=" ORIGIN EXPECT EXPECT_FAILED DRAIN_IDLE_TIMEOUT CONSUMER_CPUS CONSUMER_MEM CONSUMER_ARGS "

compose() { docker compose -f docker-compose.yml --profile load "$@"; }

die() { echo "ERROR: $*" >&2; exit 2; }

# Example names double as image names, hence the lowercase-only charset.
list_examples() {
  local dir
  for dir in ../examples/*/; do
    [ -f "$dir/Dockerfile" ] && basename "$dir"
  done
}

require_example() {
  local name="$1"
  [[ "$name" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die "invalid example name '$name' (lowercase letters, digits, '-', '_')"
  if [ ! -f "../examples/$name/Dockerfile" ]; then
    echo "ERROR: examples/$name/Dockerfile not found. Available examples:" >&2
    list_examples | sed 's/^/  /' >&2
    exit 2
  fi
}

# Exports perf.env values for keys not already set in the environment, so command-line env vars
# win over the file and script defaults apply to whatever is left unset.
load_perf_env() {
  local file="$1" line key value
  [ -f "$file" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in ''|'#'*) continue ;; esac
    [[ "$line" == *=* ]] || die "$file: expected KEY=VALUE, got '$line'"
    key="${line%%=*}"
    value="${line#*=}"
    [[ "$perf_env_keys" == *" $key "* ]] || die "$file: unknown key '$key' (allowed:$perf_env_keys)"
    [ -n "${!key+x}" ] || export "$key=$value"
  done < "$file"
}

# Same charset altair enforces (shared/sanitize_origin.go); also keeps the sed render below safe.
require_origin() {
  [[ "$1" =~ ^[A-Za-z0-9_-]+$ ]] || die "invalid ORIGIN '$1' (letters, digits, '-', '_')"
}

render_requests() {
  local origin="$1" name
  require_origin "$origin"
  mkdir -p "$run_dir"
  for name in smoke heavy; do
    sed "s/{{ORIGIN}}/$origin/g" "request-$name.json" > "$run_dir/request-$name.json"
  done
}

# Prints the consumer's received count, probed from the altair container over the stack network
# so example images need no HTTP tooling of their own. Empty when the consumer is not up yet.
consumer_received() {
  compose exec -T altair wget -qO- http://consumer:9090/stats 2>/dev/null \
    | grep -o '"received":[0-9]*' | grep -o '[0-9]*$' || true
}

wait_for_consumer() {
  local attempt
  for attempt in $(seq 1 30); do
    [ -n "$(consumer_received)" ] && return 0
    sleep 1
  done
  die "consumer did not answer GET /stats within 30s"
}

# Builds the given services (all when none given) inside the dedicated builder, starting from a
# clean compose project.
perf_setup() {
  perf_teardown_stack

  if ! docker buildx inspect "$perf_builder" >/dev/null 2>&1; then
    docker image inspect "$buildkit_image" >/dev/null 2>&1 || touch "$buildkit_marker"
    docker buildx create --name "$perf_builder" --driver docker-container >/dev/null
  fi
  export BUILDX_BUILDER="$perf_builder"
  compose build "$@"
}

# Removes one run's containers, network and volumes; keeps images and builder cache so the next
# example in the same invocation builds fast.
perf_teardown_stack() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}

# Removes everything perf_setup and the runs created. KEEP=1 skips it (run cleanup.sh later).
perf_cleanup() {
  if [ "${KEEP:-0}" = "1" ]; then
    echo "KEEP=1: stack left running; remove everything later with ./perf/cleanup.sh"
    return
  fi

  echo "Cleaning up perf resources (containers, network, volumes, images, builder + build cache)..."
  perf_teardown_stack
  # By tag rather than `down --rmi`: covers every example built in this or an interrupted run.
  docker images -q --filter "reference=altair*:perf" | sort -u | xargs -r docker image rm -f >/dev/null 2>&1 || true
  docker buildx rm -f "$perf_builder" >/dev/null 2>&1 || true
  if [ -f "$buildkit_marker" ]; then
    docker image rm "$buildkit_image" >/dev/null 2>&1 || true
    rm -f "$buildkit_marker"
  fi
  rm -rf "$run_dir"
}
