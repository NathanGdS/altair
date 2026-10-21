# End-to-end delivery validation inside the resource-limited Docker stack: publishes to altair,
# then checks what the example consumer's webhook received against that example's perf.env
# expectations (see examples/README.md).
#
# Usage: .\perf\validate.ps1 [-Mode smoke|heavy] [-Example <name>|all]   (defaults: smoke 01-consumer)
#   smoke: fixed 500 requests.
#   heavy: duration-based load (CONNECTIONS/DURATION/PIPELINING, default -c 20 -d 5 -p 5).
#   all:   every examples/*/ with a Dockerfile, each on a fresh stack, then a PASS/FAIL summary.
# Env (overrides perf.env): ORIGIN, EXPECT, EXPECT_FAILED, DRAIN_IDLE_TIMEOUT, CONSUMER_CPUS,
# CONSUMER_MEM, CONSUMER_ARGS. Other env: ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS, LOADER_MEM, KEEP=1.
param(
    [ValidateSet("smoke", "heavy")]
    [string]$Mode = "smoke",
    [string]$Example = "01-consumer"
)
$ErrorActionPreference = "Stop"

. "$PSScriptRoot/lib.ps1"

$SmokeCount = 500

# Validates one example; returns $true on PASS. Expects EXAMPLE and the perf.env keys to already be
# exported (compose interpolates them).
function Invoke-ExampleValidation([string]$name) {
    $origin = Get-EnvOrDefault "ORIGIN" "stress-test"
    $expect = Get-EnvOrDefault "EXPECT" "auto"
    $expectFailed = Get-EnvOrDefault "EXPECT_FAILED" "empty"
    $idleTimeoutText = Get-EnvOrDefault "DRAIN_IDLE_TIMEOUT" "30"
    $env:ORIGIN = $origin

    if (@("auto", "exact", "at-least", "none") -notcontains $expect) { throw "EXPECT must be auto|exact|at-least|none, got '$expect'" }
    if (@("empty", "non-empty", "any") -notcontains $expectFailed) { throw "EXPECT_FAILED must be empty|non-empty|any, got '$expectFailed'" }
    if ($idleTimeoutText -notmatch '^[0-9]+$') { throw "DRAIN_IDLE_TIMEOUT must be a number of seconds, got '$idleTimeoutText'" }
    if ($expect -eq "auto") {
        if ($Mode -eq "smoke") { $expect = "exact" } else { $expect = "at-least" }
    }

    if ($Mode -eq "smoke") {
        $loadArgs = @("-c", "10", "-a", "$SmokeCount")
    }
    else {
        $loadArgs = @(
            "-c", (Get-EnvOrDefault "CONNECTIONS" "20"),
            "-d", (Get-EnvOrDefault "DURATION" "5"),
            "-p", (Get-EnvOrDefault "PIPELINING" "5")
        )
    }

    Write-Host "[$name] ${Mode}: ORIGIN=$origin EXPECT=$expect EXPECT_FAILED=$expectFailed (altair cpus=$(Get-EnvOrDefault 'ALTAIR_CPUS' '2'), mem=$(Get-EnvOrDefault 'ALTAIR_MEM' '4g'); consumer cpus=$(Get-EnvOrDefault 'CONSUMER_CPUS' '1'), mem=$(Get-EnvOrDefault 'CONSUMER_MEM' '512m'))"
    Write-Requests $origin
    Invoke-PerfSetup | Out-Host
    Invoke-Compose up -d --wait altair | Out-Host
    Invoke-Compose up -d consumer | Out-Host
    Wait-Consumer

    Write-Host "Firing $Mode load via autocannon..."
    $resultJson = Invoke-Compose run --rm -T loader @loadArgs --json `
        -m POST -H "Content-Type: application/json" -i "/requests/request-$Mode.json" `
        http://altair:8080/publish
    $result = $resultJson | ConvertFrom-Json

    $accepted = [int64]$result.'2xx'
    $nonSuccess = [int64]$result.non2xx + [int64]$result.errors + [int64]$result.timeouts
    Write-Host "autocannon: $accepted 2xx responses, $nonSuccess non-2xx/errors/timeouts, $($result.requests.sent) requests sent"

    if ($accepted -le 0) {
        Write-Host "FAIL: autocannon reported 0 successful (2xx) publishes"
        return $false
    }
    if ($Mode -eq "smoke" -and $accepted -ne $SmokeCount) {
        Write-Host "FAIL: altair accepted $accepted/$SmokeCount smoke publishes"
        return $false
    }

    Write-Host "Waiting for deliveries to drain (progress logged every ~10s)..."
    # Keep polling while `received` is still climbing; give up only once it stalls for
    # idleTimeout or the absolute ceiling is hit (a heavy run can take minutes to drain).
    $idleTimeout = [TimeSpan]::FromSeconds([int]$idleTimeoutText)
    $absoluteMax = [TimeSpan]::FromSeconds(1800)
    $started = Get-Date
    $received = 0
    $lastReceived = -1
    $lastProgress = Get-Date
    $lastLog = Get-Date
    while ($true) {
        Start-Sleep -Seconds 2
        $received = Get-ConsumerReceived
        if ($null -eq $received) { $received = 0 }

        if ($received -ne $lastReceived) {
            $lastReceived = $received
            $lastProgress = Get-Date
        }
        if ($received -ge $accepted) { break }

        if (((Get-Date) - $lastLog).TotalSeconds -ge 10) {
            Write-Host "  ... $received/$accepted delivered so far"
            $lastLog = Get-Date
        }
        if (((Get-Date) - $lastProgress) -ge $idleTimeout) {
            Write-Host "  stalled: no new deliveries for $($idleTimeout.TotalSeconds)s"
            break
        }
        if (((Get-Date) - $started) -ge $absoluteMax) {
            Write-Host "  gave up after $($absoluteMax.TotalSeconds)s"
            break
        }
    }

    $failedFiles = Invoke-Compose exec -T altair find /app/deliveries/failed -type f
    $failedCount = @($failedFiles | Where-Object { $_ }).Count

    Write-Host "Resource usage at the end of the run:"
    Show-ContainerStats altair consumer | Out-Host

    # Delivery is at-least-once (a webhook timeout triggers a retry even if the original attempt
    # eventually succeeded), so `received` may exceed `accepted` without that being a bug; that is
    # why heavy runs default to EXPECT=at-least.
    $problems = @()
    if ($expect -eq "exact" -and $received -ne $accepted) { $problems += "received $received, expected exactly $accepted" }
    if ($expect -eq "at-least" -and $received -lt $accepted) { $problems += "received $received, expected at least $accepted" }
    if ($expectFailed -eq "empty" -and $failedCount -ne 0) { $problems += "deliveries/failed has $failedCount file(s), expected empty" }
    if ($expectFailed -eq "non-empty" -and $failedCount -eq 0) { $problems += "deliveries/failed empty, expected non-empty" }

    $summary = "received $received/$accepted ($expect), deliveries/failed $failedCount file(s) ($expectFailed)"
    if ($problems.Count -eq 0) {
        Write-Host "PASS: $summary"
        return $true
    }
    Write-Host "FAIL: $summary"
    $problems | ForEach-Object { Write-Host "  - $_" }
    return $false
}

if ($Example -eq "all") {
    $examples = @(Get-Examples)
    if ($examples.Count -eq 0) { throw "no examples/*/Dockerfile found" }
    if ((Get-EnvOrDefault "KEEP" "0") -eq "1") { throw "KEEP=1 is not supported with 'all' (each example replaces the previous stack)" }
}
else {
    Assert-Example $Example
    $examples = @($Example)
}

$results = @()
$failures = 0
Push-Location $PSScriptRoot
try {
    foreach ($name in $examples) {
        # perf.env values are exported only for this example and restored afterwards, so they
        # never leak into the next one; command-line env vars are left untouched.
        $managedKeys = @("EXAMPLE") + $PerfEnvKeys
        $saved = @{}
        foreach ($key in $managedKeys) { $saved[$key] = [Environment]::GetEnvironmentVariable($key) }
        $passed = $false
        try {
            $env:EXAMPLE = $name
            $fileValues = Read-PerfEnv (Join-Path $ExamplesDir "$name/perf.env")
            foreach ($key in $fileValues.Keys) { [Environment]::SetEnvironmentVariable($key, $fileValues[$key]) }
            # Last value only: any stray pipeline output must never turn a FAIL into a truthy array.
            $passed = @(Invoke-ExampleValidation $name)[-1] -eq $true
        }
        catch {
            Write-Host "FAIL: $($_.Exception.Message)"
        }
        finally {
            if ((Get-EnvOrDefault "KEEP" "0") -ne "1") { Remove-PerfStack }
            foreach ($key in $managedKeys) { [Environment]::SetEnvironmentVariable($key, $saved[$key]) }
        }
        if ($passed) { $results += "{0,-24} PASS" -f $name }
        else { $results += "{0,-24} FAIL" -f $name; $failures++ }
    }

    if ($Example -eq "all") {
        Write-Host ""
        Write-Host "Summary (${Mode}):"
        $results | ForEach-Object { Write-Host "  $_" }
    }
}
finally {
    Invoke-PerfCleanup
    Pop-Location
}
if ($failures -gt 0) { exit 1 }
exit 0
