# Shared helpers for the perf scripts (sourced, not executed). Every Docker resource a run
# creates is scoped so perf_cleanup can remove all of it without touching anything else on the
# machine: containers/network/volumes/images via the compose project, and build cache + base
# images inside a dedicated buildx builder that is deleted as a whole.

export MSYS_NO_PATHCONV=1 # Git Bash: keep container paths like /requests/... untouched

perf_builder="altair-perf"
buildkit_image="moby/buildkit:buildx-stable-1"
# Marks that the buildkit image was pulled by us (not present before), so cleanup may remove it
# even when the run that pulled it was interrupted.
buildkit_marker=".buildkit-pulled"

compose() { docker compose -f docker-compose.yml --profile load "$@"; }

# Builds the images inside the dedicated builder, starting from a clean compose project.
perf_setup() {
  compose down -v --remove-orphans --rmi all >/dev/null 2>&1 || true

  if ! docker buildx inspect "$perf_builder" >/dev/null 2>&1; then
    docker image inspect "$buildkit_image" >/dev/null 2>&1 || touch "$buildkit_marker"
    docker buildx create --name "$perf_builder" --driver docker-container >/dev/null
  fi
  export BUILDX_BUILDER="$perf_builder"
  compose build
}

# Removes everything perf_setup and the run created. KEEP=1 skips it (run cleanup.sh later).
perf_cleanup() {
  if [ "${KEEP:-0}" = "1" ]; then
    echo "KEEP=1: stack left running; remove everything later with ./perf/cleanup.sh"
    return
  fi

  echo "Cleaning up perf resources (containers, network, volumes, images, builder + build cache)..."
  compose down -v --remove-orphans --rmi all >/dev/null 2>&1 || true
  docker buildx rm -f "$perf_builder" >/dev/null 2>&1 || true
  if [ -f "$buildkit_marker" ]; then
    docker image rm "$buildkit_image" >/dev/null 2>&1 || true
    rm -f "$buildkit_marker"
  fi
}
