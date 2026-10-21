# Fires an autocannon load at a resource-limited altair container and prints the result plus
# the container's resource usage. No consumer is involved. Tunables (env): CONNECTIONS,
# DURATION, PIPELINING, REQUEST (smoke|heavy), ORIGIN, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS,
# LOADER_MEM, KEEP=1 to leave the stack running afterwards.
#
# -Interactive runs in three phases so the altair UI can be watched (published on
# 127.0.0.1:ALTAIR_UI_PORT, default 8080): 1) altair up, wait for Enter; 2) fire the load, then
# Enter fires another round on the same stack or q finishes; 3) clean up.
param(
    [switch]$Interactive
)
$ErrorActionPreference = "Stop"

. "$PSScriptRoot/lib.ps1"
$PerfInteractive = [bool]$Interactive

$connections = Get-EnvOrDefault "CONNECTIONS" "20"
$duration = Get-EnvOrDefault "DURATION" "5"
$pipelining = Get-EnvOrDefault "PIPELINING" "5"
$request = Get-EnvOrDefault "REQUEST" "heavy"
if (@("smoke", "heavy") -notcontains $request) { throw "REQUEST must be smoke|heavy, got '$request'" }
if ($Interactive) { Assert-InteractivePorts }

function Invoke-Load {
    Write-Host "Firing load: -c $connections -d $duration -p $pipelining ($request payload)..."
    Invoke-ComposeNoStdin run --rm -T loader -c $connections -d $duration -p $pipelining `
        -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" `
        http://altair:8080/publish | Out-Host

    Write-Host "altair resource usage right after the load:"
    Show-ContainerStats altair | Out-Host
}

Push-Location $PSScriptRoot
try {
    Write-Host "Building images and starting altair (cpus=$(Get-EnvOrDefault 'ALTAIR_CPUS' '2'), mem=$(Get-EnvOrDefault 'ALTAIR_MEM' '4g'))..."
    Write-Requests (Get-EnvOrDefault "ORIGIN" "stress-test")
    Invoke-PerfSetup altair loader
    Invoke-Compose up -d --wait altair

    if (-not $Interactive) {
        Invoke-Load
    }
    else {
        Write-Host "[1/3] altair is up."
        Show-UiUrls
        if (Read-PerfPrompt "Press Enter to fire the load (q to finish and clean up)... ") {
            $round = 1
            while ($true) {
                Write-Host "[2/3] Round $round"
                Invoke-Load
                Show-UiUrls
                if (-not (Read-PerfPrompt "Enter = fire another round, q = finish and clean up: ")) { break }
                $round++
            }
            Write-Host "[3/3] Finishing after $round round(s)."
        }
    }
}
finally {
    Invoke-PerfCleanup
    Pop-Location
}
