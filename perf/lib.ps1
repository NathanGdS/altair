# Shared helpers for the perf scripts (dot-sourced, not executed; cwd must be perf/). Every Docker
# resource a run creates is scoped so Invoke-PerfCleanup can remove all of it without touching
# anything else on the machine: containers/network/volumes via the compose project, images by
# their `:perf` tag, and build cache + base images inside a dedicated buildx builder deleted as a
# whole.

$PerfBuilder = "altair-perf"
$BuildkitImage = "moby/buildkit:buildx-stable-1"
# Marks that the buildkit image was pulled by us (not present before), so cleanup may remove it
# even when the run that pulled it was interrupted.
$BuildkitMarker = Join-Path $PSScriptRoot ".buildkit-pulled"
$RunDir = Join-Path $PSScriptRoot ".run"
$ExamplesDir = Join-Path $PSScriptRoot "../examples"

# Keys an examples/<name>/perf.env may set (see examples/README.md).
$PerfEnvKeys = @("ORIGIN", "EXPECT", "EXPECT_FAILED", "DRAIN_IDLE_TIMEOUT", "CONSUMER_CPUS", "CONSUMER_MEM", "CONSUMER_ARGS")

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
    docker compose @(Get-ComposeFiles) --profile load @args
    if ($LASTEXITCODE -ne 0) { throw "docker compose $args failed with exit code $LASTEXITCODE" }
}

# Same as Invoke-Compose with stdin closed: `compose run`/`exec` forward stdin to the container,
# which would swallow the interactive Enter/q (or piped input).
function Invoke-ComposeNoStdin {
    $ErrorActionPreference = "Continue"
    $null | docker compose @(Get-ComposeFiles) --profile load @args
    if ($LASTEXITCODE -ne 0) { throw "docker compose $args failed with exit code $LASTEXITCODE" }
}

# Set to $true by the scripts' -Interactive switch: publishes the UI/stats ports (see
# docker-compose.ui.yml) only for interactive runs.
$PerfInteractive = $false

function Get-ComposeFiles {
    $files = @("-f", "docker-compose.yml")
    if ($PerfInteractive) { $files += @("-f", "docker-compose.ui.yml") }
    return $files
}

function Assert-InteractivePorts {
    foreach ($name in @("ALTAIR_UI_PORT", "CONSUMER_STATS_PORT")) {
        $value = [Environment]::GetEnvironmentVariable($name)
        if ($value -and $value -notmatch '^[0-9]+$') { throw "$name must be a port number, got '$value'" }
    }
}

function Show-UiUrls([switch]$WithConsumer) {
    Write-Host "      altair UI:      http://localhost:$(Get-EnvOrDefault 'ALTAIR_UI_PORT' '8080')"
    if ($WithConsumer) { Write-Host "      consumer stats: http://localhost:$(Get-EnvOrDefault 'CONSUMER_STATS_PORT' '9090')/stats" }
}

# Prints the prompt and waits for a line. $true to go on (Enter); $false on "q" or EOF, so a run
# with no console attached (e.g. CI) finishes and cleans up instead of hanging.
function Read-PerfPrompt([string]$message) {
    Write-Host $message -NoNewline
    if ([Console]::IsInputRedirected) { $answer = [Console]::In.ReadLine() } else { $answer = Read-Host }
    if ($null -eq $answer) {
        Write-Host ""
        return $false
    }
    return @("q", "Q") -notcontains $answer.Trim()
}

# Runs a docker command whose failure is expected/harmless (resource may not exist).
function Invoke-DockerQuiet {
    $ErrorActionPreference = "Continue"
    docker @args 2>$null | Out-Null
}

# Example names double as image names, hence the lowercase-only charset.
function Get-Examples {
    Get-ChildItem -Directory $ExamplesDir |
        Where-Object { Test-Path (Join-Path $_.FullName "Dockerfile") } |
        ForEach-Object { $_.Name }
}

function Assert-Example([string]$name) {
    if ($name -cnotmatch '^[a-z0-9][a-z0-9_-]*$') {
        throw "invalid example name '$name' (lowercase letters, digits, '-', '_')"
    }
    if (-not (Test-Path (Join-Path $ExamplesDir "$name/Dockerfile"))) {
        $available = (Get-Examples | ForEach-Object { "  $_" }) -join [Environment]::NewLine
        throw "examples/$name/Dockerfile not found. Available examples:$([Environment]::NewLine)$available"
    }
}

# Returns perf.env values for keys not already set in the environment, so command-line env vars
# win over the file and script defaults apply to whatever is left unset.
function Read-PerfEnv([string]$file) {
    $values = @{}
    if (-not (Test-Path $file)) { return $values }
    foreach ($line in Get-Content $file) {
        $line = $line.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { continue }
        $separator = $line.IndexOf("=")
        if ($separator -lt 1) { throw "${file}: expected KEY=VALUE, got '$line'" }
        $key = $line.Substring(0, $separator)
        if ($PerfEnvKeys -notcontains $key) { throw "${file}: unknown key '$key' (allowed: $($PerfEnvKeys -join ' '))" }
        if ($null -eq [Environment]::GetEnvironmentVariable($key)) { $values[$key] = $line.Substring($separator + 1) }
    }
    return $values
}

# Same charset altair enforces (shared/sanitize_origin.go).
function Assert-Origin([string]$origin) {
    if ($origin -notmatch '^[A-Za-z0-9_-]+$') { throw "invalid ORIGIN '$origin' (letters, digits, '-', '_')" }
}

function Write-Requests([string]$origin) {
    Assert-Origin $origin
    New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
    foreach ($name in @("smoke", "heavy")) {
        $content = (Get-Content -Raw (Join-Path $PSScriptRoot "request-$name.json")).Replace("{{ORIGIN}}", $origin)
        # UTF-8 without BOM: autocannon sends the file verbatim and altair rejects a leading BOM.
        [IO.File]::WriteAllText((Join-Path $RunDir "request-$name.json"), $content, (New-Object Text.UTF8Encoding $false))
    }
}

# Returns the consumer's received count, probed from the altair container over the stack network
# so example images need no HTTP tooling of their own. $null when the consumer is not up yet.
function Get-ConsumerReceived {
    $ErrorActionPreference = "Continue"
    $stats = $null | docker compose -f docker-compose.yml exec -T altair wget -qO- http://consumer:9090/stats 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $stats) { return $null }
    return [int64]($stats | ConvertFrom-Json).received
}

function Wait-Consumer {
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        if ($null -ne (Get-ConsumerReceived)) { return }
        Start-Sleep -Seconds 1
    }
    throw "consumer did not answer GET /stats within 30s"
}

function Show-ContainerStats {
    $ErrorActionPreference = "Continue"
    $ids = Invoke-Compose ps -q @args
    docker stats --no-stream $ids
}

# Builds the given services (all when none given) inside the dedicated builder, starting from a
# clean compose project.
function Invoke-PerfSetup {
    $ErrorActionPreference = "Continue"
    Remove-PerfStack

    Invoke-DockerQuiet info
    if ($LASTEXITCODE -ne 0) { throw "Docker daemon is not reachable" }
    Invoke-DockerQuiet buildx inspect $PerfBuilder
    if ($LASTEXITCODE -ne 0) {
        Invoke-DockerQuiet image inspect $BuildkitImage
        if ($LASTEXITCODE -ne 0) { New-Item -ItemType File -Force -Path $BuildkitMarker | Out-Null }
        docker buildx create --name $PerfBuilder --driver docker-container | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "failed to create buildx builder $PerfBuilder" }
    }
    $env:BUILDX_BUILDER = $PerfBuilder
    Invoke-Compose build @args
}

# Removes one run's containers, network and volumes; keeps images and builder cache so the next
# example in the same invocation builds fast.
function Remove-PerfStack {
    Invoke-DockerQuiet compose -f docker-compose.yml --profile load down -v --remove-orphans
}

# Removes everything Invoke-PerfSetup and the runs created. KEEP=1 skips it (run cleanup.ps1 later).
function Invoke-PerfCleanup([switch]$Force) {
    if (-not $Force -and (Get-EnvOrDefault "KEEP" "0") -eq "1") {
        Write-Host "KEEP=1: stack left running; remove everything later with .\perf\cleanup.ps1"
        return
    }

    $ErrorActionPreference = "Continue"
    Write-Host "Cleaning up perf resources (containers, network, volumes, images, builder + build cache)..."
    Remove-PerfStack
    # By tag rather than `down --rmi`: covers every example built in this or an interrupted run.
    $images = docker images -q --filter "reference=altair*:perf" | Sort-Object -Unique
    if ($images) { Invoke-DockerQuiet image rm -f @images }
    Invoke-DockerQuiet buildx rm -f $PerfBuilder
    if (Test-Path $BuildkitMarker) {
        Invoke-DockerQuiet image rm $BuildkitImage
        # Keep the marker while the image still exists (e.g. the daemon was down), so a later
        # cleanup.ps1 still knows the image is ours to remove.
        Invoke-DockerQuiet image inspect $BuildkitImage
        if ($LASTEXITCODE -ne 0) { Remove-Item -Force $BuildkitMarker }
    }
    if (Test-Path $RunDir) { Remove-Item -Recurse -Force $RunDir }
}
