# Shared helpers for the perf scripts (dot-sourced, not executed). Every Docker resource a run
# creates is scoped so Invoke-PerfCleanup can remove all of it without touching anything else on
# the machine: containers/network/volumes/images via the compose project, and build cache + base
# images inside a dedicated buildx builder that is deleted as a whole.

$PerfBuilder = "altair-perf"
$BuildkitImage = "moby/buildkit:buildx-stable-1"
# Marks that the buildkit image was pulled by us (not present before), so cleanup may remove it
# even when the run that pulled it was interrupted.
$BuildkitMarker = Join-Path $PSScriptRoot ".buildkit-pulled"

function Get-EnvOrDefault([string]$name, [string]$default) {
    $value = [Environment]::GetEnvironmentVariable($name)
    if ([string]::IsNullOrEmpty($value)) { return $default }
    return $value
}

# Docker reports progress on stderr; under Windows PowerShell 5.1 that becomes a terminating
# NativeCommandError whenever output is redirected (e.g. `*> run.log`) and the caller uses
# ErrorActionPreference=Stop. Native failures are detected via $LASTEXITCODE instead, so the
# preference is relaxed locally (assignment inside a function only affects its own scope).
function Invoke-Compose {
    $ErrorActionPreference = "Continue"
    docker compose -f docker-compose.yml --profile load @args
    if ($LASTEXITCODE -ne 0) { throw "docker compose $args failed with exit code $LASTEXITCODE" }
}

# Runs a docker command whose failure is expected/harmless (resource may not exist).
function Invoke-DockerQuiet {
    $ErrorActionPreference = "Continue"
    docker @args 2>$null | Out-Null
}

function Show-ContainerStats {
    $ErrorActionPreference = "Continue"
    $ids = Invoke-Compose ps -q @args
    docker stats --no-stream $ids
}

# Builds the images inside the dedicated builder, starting from a clean compose project.
function Invoke-PerfSetup {
    $ErrorActionPreference = "Continue"
    Invoke-DockerQuiet compose -f docker-compose.yml --profile load down -v --remove-orphans --rmi all

    Invoke-DockerQuiet buildx inspect $PerfBuilder
    if ($LASTEXITCODE -ne 0) {
        Invoke-DockerQuiet image inspect $BuildkitImage
        if ($LASTEXITCODE -ne 0) { New-Item -ItemType File -Force -Path $BuildkitMarker | Out-Null }
        docker buildx create --name $PerfBuilder --driver docker-container | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "failed to create buildx builder $PerfBuilder" }
    }
    $env:BUILDX_BUILDER = $PerfBuilder
    Invoke-Compose build
}

# Removes everything Invoke-PerfSetup and the run created. KEEP=1 skips it (run cleanup.ps1 later).
function Invoke-PerfCleanup([switch]$Force) {
    if (-not $Force -and (Get-EnvOrDefault "KEEP" "0") -eq "1") {
        Write-Host "KEEP=1: stack left running; remove everything later with .\perf\cleanup.ps1"
        return
    }

    Write-Host "Cleaning up perf resources (containers, network, volumes, images, builder + build cache)..."
    Invoke-DockerQuiet compose -f docker-compose.yml --profile load down -v --remove-orphans --rmi all
    Invoke-DockerQuiet buildx rm -f $PerfBuilder
    if (Test-Path $BuildkitMarker) {
        Invoke-DockerQuiet image rm $BuildkitImage
        Remove-Item -Force $BuildkitMarker
    }
}
