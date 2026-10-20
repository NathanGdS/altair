# End-to-end delivery validation inside the resource-limited Docker stack: publishes to altair,
# then confirms the example consumer's webhook received every accepted (2xx) publish and that
# deliveries/failed stayed empty.
#
# Modes:
#   smoke (default): fixed 500 requests, consumer must receive exactly that many.
#   heavy:           duration-based load (CONNECTIONS/DURATION/PIPELINING, default -c 20 -d 5 -p 5);
#                    received may exceed accepted (at-least-once retries), never fall short.
# Other env: ALTAIR_CPUS, ALTAIR_MEM, CONSUMER_CPUS, CONSUMER_MEM, LOADER_CPUS, LOADER_MEM, KEEP=1.
param(
    [ValidateSet("smoke", "heavy")]
    [string]$Mode = "smoke"
)
$ErrorActionPreference = "Stop"

. "$PSScriptRoot/lib.ps1"

function Get-ConsumerReceived {
    $ErrorActionPreference = "Continue"
    $stats = docker compose -f docker-compose.yml exec -T consumer wget -qO- http://localhost:9090/stats
    if ($LASTEXITCODE -ne 0 -or -not $stats) { return 0 }
    return [int64]($stats | ConvertFrom-Json).received
}

$smokeCount = 500
if ($Mode -eq "smoke") {
    $loadArgs = @("-c", "10", "-a", "$smokeCount", "-i", "/requests/request-smoke.json")
}
else {
    $loadArgs = @(
        "-c", (Get-EnvOrDefault "CONNECTIONS" "20"),
        "-d", (Get-EnvOrDefault "DURATION" "5"),
        "-p", (Get-EnvOrDefault "PIPELINING" "5"),
        "-i", "/requests/request-heavy.json"
    )
}

Push-Location $PSScriptRoot
try {
    Write-Host "Building images and starting a clean stack (altair cpus=$(Get-EnvOrDefault 'ALTAIR_CPUS' '2'), mem=$(Get-EnvOrDefault 'ALTAIR_MEM' '4g'))..."
    Invoke-PerfSetup
    Invoke-Compose up -d --wait altair consumer

    Write-Host "Firing $Mode load via autocannon..."
    $resultJson = Invoke-Compose run --rm -T loader @loadArgs --json `
        -m POST -H "Content-Type: application/json" http://altair:8080/publish
    $result = $resultJson | ConvertFrom-Json

    $accepted = [int64]$result.'2xx'
    $nonSuccess = [int64]$result.non2xx + [int64]$result.errors + [int64]$result.timeouts
    Write-Host "autocannon: $accepted 2xx responses, $nonSuccess non-2xx/errors/timeouts, $($result.requests.sent) requests sent"

    if ($accepted -le 0) {
        Write-Host "FAIL: autocannon reported 0 successful (2xx) publishes"
        exit 1
    }

    Write-Host "Waiting for deliveries to drain (progress logged every ~10s)..."
    # Keep polling while `received` is still climbing; give up only once it stalls for
    # idleTimeout or the absolute ceiling is hit (a heavy run can take minutes to drain).
    $idleTimeout = [TimeSpan]::FromSeconds(30)
    $absoluteMax = [TimeSpan]::FromSeconds(1800)
    $started = Get-Date
    $received = 0
    $lastReceived = -1
    $lastProgress = Get-Date
    $lastLog = Get-Date
    while ($true) {
        Start-Sleep -Seconds 2
        $received = Get-ConsumerReceived

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
    Show-ContainerStats altair consumer

    if ($Mode -eq "smoke") {
        if ($accepted -eq $smokeCount -and $received -eq $accepted -and $failedCount -eq 0) {
            Write-Host "PASS: consumer received $received/$smokeCount deliveries, deliveries/failed empty"
            exit 0
        }
        Write-Host "FAIL: consumer received $received/$smokeCount deliveries ($accepted accepted), deliveries/failed has $failedCount file(s)"
        exit 1
    }

    # Delivery is at-least-once (a webhook timeout triggers a retry even if the original attempt
    # eventually succeeded), so `received` may exceed `accepted` under heavy load without that
    # being a bug. The only real failures are lost messages or a non-empty deliveries/failed.
    if ($received -ge $accepted -and $failedCount -eq 0) {
        $extra = $received - $accepted
        if ($extra -gt 0) {
            Write-Host "PASS: consumer received $received/$accepted deliveries ($extra likely retry-on-timeout duplicates, deliveries/failed empty)"
        }
        else {
            Write-Host "PASS: consumer received $received/$accepted deliveries, deliveries/failed empty"
        }
        exit 0
    }
    Write-Host "FAIL: consumer received $received/$accepted deliveries, deliveries/failed has $failedCount file(s)"
    exit 1
}
finally {
    Invoke-PerfCleanup
    Pop-Location
}
