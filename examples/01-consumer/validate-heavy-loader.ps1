# Heavy end-to-end validation: builds altair + the example consumer, fires a heavy
# autocannon load (same concurrency/pipelining profile as the project root's loader.ps1,
# duration-based rather than a fixed request count) at /publish, then confirms the
# consumer's webhook received exactly as many deliveries as altair actually accepted
# (2xx responses).
$ErrorActionPreference = "Stop"

$scriptDir = $PSScriptRoot
$root = Resolve-Path "$scriptDir/../.."
Push-Location $root

# Same shape as the root's request-loader.json: "stress-test": true, no explicit origin,
# so the server defaults origin to "stress-test" (handlers/publish_handler.go). The example
# consumer below is registered against that same origin.
$requestFile = "$root/request-loader.json"
$origin = "stress-test"
$serverProc = $null
$consumerProc = $null

try {
    Write-Host "Building altair and example consumer..."
    # Windows requires a recognized executable extension for Start-Process (ShellExecute) to
    # launch a binary by path; `go build -o bin/altair` (no extension) produces a file
    # Start-Process cannot invoke, so we build with .exe explicitly on this platform.
    go build -o bin/altair.exe ./main.go
    go build -o bin/example-consumer.exe ./examples/01-consumer

    Write-Host "Resetting runtime state for a clean heavy-load run..."
    $requiredDirs = @(
        "messages/ready",
        "messages/processed",
        "messages/trash",
        "data",
        "deliveries/pending",
        "deliveries/failed"
    )
    foreach ($dir in $requiredDirs) {
        New-Item -ItemType Directory -Force -Path "$root/$dir" | Out-Null
        Get-ChildItem -Path "$root/$dir" -File -ErrorAction SilentlyContinue | Remove-Item -Force
    }
    Remove-Item -Force -ErrorAction SilentlyContinue -Path "$root/data/altair.db"

    Write-Host "Starting altair..."
    $serverProc = Start-Process -FilePath "$root/bin/altair.exe" -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 2

    Write-Host "Starting example consumer (origin: $origin)..."
    $consumerProc = Start-Process -FilePath "$root/bin/example-consumer.exe" -ArgumentList "-origin", $origin -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 2

    Write-Host "Firing heavy load via autocannon (loader.ps1 profile, -d bumped to 60s)..."
    $resultJson = npx autocannon -c 20 -d 60 -p 20 -m POST -H "Content-Type: application/json" -i "$requestFile" --json http://localhost:8080/publish
    $result = $resultJson | ConvertFrom-Json

    $accepted = $result.'2xx'
    $nonSuccess = [int64]$result.non2xx + [int64]$result.errors + [int64]$result.timeouts
    Write-Host "autocannon: $accepted 2xx responses, $nonSuccess non-2xx/errors/timeouts, $($result.requests.sent) requests sent"

    if ($accepted -le 0) {
        Write-Host "FAIL: autocannon reported 0 successful (2xx) publishes"
        exit 1
    }

    Write-Host "Waiting for deliveries to drain (this scales with the load above; progress logged every ~10s)..."
    # A fixed wall-clock deadline doesn't work at this scale: a heavy enough run can produce
    # far more accepted publishes than the bounded delivery pool can drain in any short window.
    # Instead, keep polling as long as `received` is still climbing, and only give up once it
    # stalls (no progress for $idleTimeoutSeconds) or a generous absolute ceiling is hit.
    $idleTimeoutSeconds = 30
    $absoluteMaxSeconds = 1800
    $started = Get-Date
    $received = 0
    $lastReceived = -1
    $lastProgressAt = Get-Date
    $lastLogAt = Get-Date
    while ($true) {
        Start-Sleep -Seconds 2
        try {
            $stats = Invoke-RestMethod -Uri "http://localhost:9090/stats"
            $received = $stats.received
        } catch {}

        if ($received -ne $lastReceived) {
            $lastReceived = $received
            $lastProgressAt = Get-Date
        }
        if ($received -ge $accepted) { break }

        if (((Get-Date) - $lastLogAt).TotalSeconds -ge 10) {
            Write-Host "  ... $received/$accepted delivered so far"
            $lastLogAt = Get-Date
        }
        if (((Get-Date) - $lastProgressAt).TotalSeconds -ge $idleTimeoutSeconds) {
            Write-Host "  stalled: no new deliveries for ${idleTimeoutSeconds}s"
            break
        }
        if (((Get-Date) - $started).TotalSeconds -ge $absoluteMaxSeconds) {
            Write-Host "  gave up after ${absoluteMaxSeconds}s"
            break
        }
    }

    $failedCount = (Get-ChildItem -Path "$root/deliveries/failed" -File -ErrorAction SilentlyContinue | Measure-Object).Count

    # Delivery is at-least-once by design (design spec: a webhook timeout triggers a retry
    # even if the original attempt eventually succeeds server-side), so `received` may exceed
    # `accepted` under heavy load without that being a bug -- especially here, where altair,
    # the example consumer, and autocannon are all fighting for CPU on one machine, which can
    # push the 5s DeliveryHTTPTimeout even for a webhook handler that's instant on its own.
    # The only real failure is `received` falling short (lost messages) or a non-empty
    # deliveries/failed (a delivery that exhausted retries and was never recovered).
    if ($received -ge $accepted -and $failedCount -eq 0) {
        $extra = $received - $accepted
        if ($extra -gt 0) {
            Write-Host "PASS: consumer received $received/$accepted deliveries ($extra likely retry-on-timeout duplicates, deliveries/failed empty)"
        } else {
            Write-Host "PASS: consumer received $received/$accepted deliveries, deliveries/failed empty"
        }
        exit 0
    } else {
        Write-Host "FAIL: consumer received $received/$accepted deliveries, deliveries/failed has $failedCount file(s)"
        exit 1
    }
}
finally {
    if ($serverProc) { Stop-Process -Id $serverProc.Id -Force -ErrorAction SilentlyContinue }
    if ($consumerProc) { Stop-Process -Id $consumerProc.Id -Force -ErrorAction SilentlyContinue }
    Pop-Location
}
