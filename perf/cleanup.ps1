# Removes every Docker resource created by the perf scripts (useful after KEEP=1 or an
# interrupted run). Leaves unrelated containers, images, volumes and build cache untouched.
$ErrorActionPreference = "Stop"

Push-Location $PSScriptRoot
try {
    . "$PSScriptRoot/lib.ps1"
    Invoke-PerfCleanup -Force
}
finally {
    Pop-Location
}
