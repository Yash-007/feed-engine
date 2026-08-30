# Task Scheduler wrapper. Adds its own random delay on top of the binary's
# start_jitter_max_sec, so runs never land on the same minute twice.
#
# Register it with scripts\install-task.ps1 once the first manual run works.
#
# Headed Chrome needs an unlocked desktop session — the scheduled task is
# registered to run only while you are logged in, on purpose.

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

if ($env:FEED_ENGINE_BIN) { $Bin = $env:FEED_ENGINE_BIN }
else { $Bin = Join-Path $Root 'bin\feed-engine.exe' }

if ($env:FEED_ENGINE_CONFIG) { $Config = $env:FEED_ENGINE_CONFIG }
else { $Config = Join-Path $Root 'config.yaml' }

if ($env:FEED_ENGINE_MAX_DELAY) { $MaxExtraDelay = [int]$env:FEED_ENGINE_MAX_DELAY }
else { $MaxExtraDelay = 600 }

$Lock = Join-Path $env:TEMP 'feed-engine.lock'

if (-not (Test-Path $Bin)) {
    Write-Error "build it first: go build -o bin\feed-engine.exe .\cmd\feed-engine"
    exit 1
}

# One run at a time. A 13-minute scroll session must not overlap the next tick.
if (Test-Path $Lock) {
    $held = $null
    try { $held = [int](Get-Content $Lock -ErrorAction Stop | Select-Object -First 1) } catch { $held = $null }
    if ($held -and (Get-Process -Id $held -ErrorAction SilentlyContinue)) {
        Write-Host "$(Get-Date -Format s) another run is still going, skipping"
        exit 0
    }
    Remove-Item $Lock -Force -ErrorAction SilentlyContinue
}

$PID | Set-Content $Lock -Encoding ascii
try {
    Start-Sleep -Seconds (Get-Random -Minimum 0 -Maximum ($MaxExtraDelay + 1))
    & $Bin -config $Config @args
    exit $LASTEXITCODE
}
finally {
    Remove-Item $Lock -Force -ErrorAction SilentlyContinue
}
