#!/usr/bin/env bash
# Removes every Docker resource created by the perf scripts (useful after KEEP=1 or an
# interrupted run). Leaves unrelated containers, images, volumes and build cache untouched.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

KEEP=0 perf_cleanup
