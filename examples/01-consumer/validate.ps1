# Heavy end-to-end validation: builds altair + the example consumer, fires a fixed
# number of /publish requests via autocannon, then confirms the consumer's webhook
# received exactly that many deliveries.
$ErrorActionPreference = "Stop"

$scriptDir = $PSScriptRoot
$root = Resolve-Path "$scriptDir/../.."
Push-Location $root

$requestCount = 500
$serverProc = $null
$consumerProc = $null

try {
    Write-Host "Building altair and example consumer..."
    go build -o bin/altair ./main.go
    go build -o bin/example-consumer ./examples/01-consumer

    Write-Host "Ensuring runtime directories exist (avoids startup race in workers.DeleteMakedFiles)..."
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
    }

    Write-Host "Starting altair..."
    $serverProc = Start-Process -FilePath "$root/bin/altair" -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 2

    Write-Host "Starting example consumer..."
    $consumerProc = Start-Process -FilePath "$root/bin/example-consumer" -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 2

    Write-Host "Firing $requestCount requests via autocannon..."
    npx autocannon -c 10 -a $requestCount -m POST -H "Content-Type: application/json" -i "$scriptDir/request.json" http://localhost:8080/publish

    Write-Host "Waiting for deliveries to drain..."
    $deadline = (Get-Date).AddSeconds(30)
    $received = 0
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 2
        try {
            $stats = Invoke-RestMethod -Uri "http://localhost:9090/stats"
            $received = $stats.received
            if ($received -ge $requestCount) { break }
        } catch {}
    }

    if ($received -eq $requestCount) {
        Write-Host "PASS: consumer received $received/$requestCount deliveries"
        exit 0
    } else {
        Write-Host "FAIL: consumer received $received/$requestCount deliveries"
        exit 1
    }
}
finally {
    if ($serverProc) { Stop-Process -Id $serverProc.Id -Force -ErrorAction SilentlyContinue }
    if ($consumerProc) { Stop-Process -Id $consumerProc.Id -Force -ErrorAction SilentlyContinue }
    Pop-Location
}
