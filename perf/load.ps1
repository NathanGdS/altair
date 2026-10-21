# Fires an autocannon load at a resource-limited altair container with an example consumer
# (EXAMPLE, default 01-consumer) registered for the load's origin, so every publish also flows
# through to webhook deliveries. Prints the autocannon result, the consumer's received count and
# the containers' resource usage; it does not wait for the deliveries to drain or judge them (that
# is validate.ps1). Tunables (env): EXAMPLE, CONNECTIONS, DURATION, PIPELINING, REQUEST
# (smoke|heavy), ORIGIN, any perf.env key, ALTAIR_CPUS, ALTAIR_MEM, LOADER_CPUS, LOADER_MEM,
# KEEP=1 to leave the stack running afterwards.
#
# -Interactive runs in three phases so the altair UI and the consumer's /stats can be watched
# (published on 127.0.0.1:ALTAIR_UI_PORT / CONSUMER_STATS_PORT, defaults 8080 / 9090): 1) altair
# and consumer up, wait for Enter; 2) fire the load, then Enter fires another round on the same
# stack or q finishes; 3) clean up.
param(
    [switch]$Interactive
)
$ErrorActionPreference = "Stop"

. "$PSScriptRoot/lib.ps1"
$PerfInteractive = [bool]$Interactive

$example = Get-EnvOrDefault "EXAMPLE" "01-consumer"
Assert-Example $example

$connections = Get-EnvOrDefault "CONNECTIONS" "20"
$duration = Get-EnvOrDefault "DURATION" "60"
$pipelining = Get-EnvOrDefault "PIPELINING" "5"
$request = Get-EnvOrDefault "REQUEST" "heavy"
if (@("smoke", "heavy") -notcontains $request) { throw "REQUEST must be smoke|heavy, got '$request'" }
if ($Interactive) { Assert-InteractivePorts }

function Invoke-Load {
    Write-Host "Firing load: -c $connections -d $duration -p $pipelining ($request payload, origin $env:ORIGIN)..."
    Invoke-ComposeNoStdin run --rm -T loader -c $connections -d $duration -p $pipelining `
        -m POST -H "Content-Type: application/json" -i "/requests/request-$request.json" `
        http://altair:8080/publish | Out-Host

    Write-Host "consumer received so far: $(Get-ConsumerReceived) (deliveries keep flowing after the load ends)"
    Write-Host "Resource usage right after the load:"
    Show-ContainerStats altair consumer | Out-Host
}

# EXAMPLE, ORIGIN and the perf.env values are exported for compose only during this run and
# restored afterwards, so they never leak into the calling PowerShell session.
$managedKeys = @("EXAMPLE") + $PerfEnvKeys
$saved = @{}
foreach ($key in $managedKeys) { $saved[$key] = [Environment]::GetEnvironmentVariable($key) }

Push-Location $PSScriptRoot
try {
    $env:EXAMPLE = $example
    $fileValues = Read-PerfEnv (Join-Path $ExamplesDir "$example/perf.env")
    foreach ($key in $fileValues.Keys) { [Environment]::SetEnvironmentVariable($key, $fileValues[$key]) }
    $env:ORIGIN = Get-EnvOrDefault "ORIGIN" "stress-test"

    Write-Host "Building images and starting altair (cpus=$(Get-EnvOrDefault 'ALTAIR_CPUS' '2'), mem=$(Get-EnvOrDefault 'ALTAIR_MEM' '4g')) and consumer $example (cpus=$(Get-EnvOrDefault 'CONSUMER_CPUS' '1'), mem=$(Get-EnvOrDefault 'CONSUMER_MEM' '512m'))..."
    Write-Requests $env:ORIGIN
    Invoke-PerfSetup altair consumer loader
    Invoke-Compose up -d --wait altair
    Invoke-Compose up -d consumer
    Wait-Consumer

    if (-not $Interactive) {
        Invoke-Load
    }
    else {
        Write-Host "[1/3] altair and consumer are up."
        Show-UiUrls -WithConsumer
        if (Read-PerfPrompt "Press Enter to fire the load (q to finish and clean up)... ") {
            $round = 1
            while ($true) {
                Write-Host "[2/3] Round $round"
                Invoke-Load
                Show-UiUrls -WithConsumer
                if (-not (Read-PerfPrompt "Enter = fire another round, q = finish and clean up: ")) { break }
                $round++
            }
            Write-Host "[3/3] Finishing after $round round(s)."
        }
    }
}
finally {
    Invoke-PerfCleanup
    foreach ($key in $managedKeys) { [Environment]::SetEnvironmentVariable($key, $saved[$key]) }
    Pop-Location
}
