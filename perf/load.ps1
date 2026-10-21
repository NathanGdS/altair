# Fires an autocannon load at a resource-limited altair container and prints the result plus
# the container's resource usage. No consumer is involved. Tunables (env): CONNECTIONS,
# DURATION, PIPELINING, REQUEST (smoke|heavy), ORIGIN, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS,
# LOADER_MEM, KEEP=1 to leave the stack running afterwards.
$ErrorActionPreference = "Stop"

. "$PSScriptRoot/lib.ps1"

$connections = Get-EnvOrDefault "CONNECTIONS" "20"
$duration = Get-EnvOrDefault "DURATION" "5"
$pipelining = Get-EnvOrDefault "PIPELINING" "5"
$request = Get-EnvOrDefault "REQUEST" "heavy"
if (@("smoke", "heavy") -notcontains $request) { throw "REQUEST must be smoke|heavy, got '$request'" }

Push-Location $PSScriptRoot
try {
    Write-Host "Building images and starting altair (cpus=$(Get-EnvOrDefault 'ALTAIR_CPUS' '2'), mem=$(Get-EnvOrDefault 'ALTAIR_MEM' '4g'))..."
    Write-Requests (Get-EnvOrDefault "ORIGIN" "stress-test")
    Invoke-PerfSetup altair loader
    Invoke-Compose up -d --wait altair

    Write-Host "Firing load: -c $connections -d $duration -p $pipelining ($request payload)..."
    Invoke-Compose run --rm -T loader -c $connections -d $duration -p $pipelining `
        -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" `
        http://altair:8080/publish

    Write-Host "altair resource usage right after the load:"
    Show-ContainerStats altair
}
finally {
    Invoke-PerfCleanup
    Pop-Location
}
