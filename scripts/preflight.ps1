# Checks everything a scheduled run needs, and says what is missing.
#
# Run it directly, or let enable/run-now call it first: the failures it catches
# are all ones that otherwise show up as a silent no-op three hours later in
# data/run.log, which is the worst possible place to discover them.
#
# Keep this file pure ASCII. Windows PowerShell 5.1 reads UTF-8-without-BOM as
# cp1252, where an em dash's trailing byte becomes a curly quote that PowerShell
# honours as a string delimiter, and the script then dies pointing at an
# unrelated line.

[CmdletBinding()]
param(
    # Exit non-zero on any problem, for callers that should stop.
    [switch] $Strict
)

$Root = Split-Path -Parent $PSScriptRoot
$problems = @()
$notes    = @()

function Test-Ok($label, $ok, $fix) {
    if ($ok) {
        Write-Host ("  [ok]   " + $label)
    } else {
        Write-Host ("  [MISS] " + $label) -ForegroundColor Yellow
        $script:problems += $fix
    }
}

Write-Host ""
Write-Host "feed-engine preflight"
Write-Host ("  root: " + $Root)
Write-Host ""

# 1. The binary. Everything else is moot without it.
$bin = Join-Path $Root 'bin\feed-engine.exe'
Test-Ok "engine binary built" (Test-Path $bin) `
    "build it:  go build -o bin\feed-engine.exe .\cmd\feed-engine"

# 2. The Playwright driver. Its absence is the classic silent failure: the
#    library reports "please install the driver first" and the run dies at once.
$driver = Join-Path $env:LOCALAPPDATA 'ms-playwright-go'
Test-Ok "playwright driver installed" (Test-Path $driver) `
    "install it once:  .\scripts\install-driver.ps1"

# 3. The claude CLI, which does the actual filtering. Task Scheduler inherits
#    the user PATH, so if it resolves here it resolves there.
$claude = Get-Command claude -ErrorAction SilentlyContinue
Test-Ok "claude CLI on PATH" ($null -ne $claude) `
    "install the claude CLI and make sure it is on your user PATH"

# 4. Chrome, driven rather than downloaded because config uses channel: chrome.
$chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe"
$chrome86 = "C:\Program Files (x86)\Google\Chrome\Application\chrome.exe"
Test-Ok "Google Chrome installed" ((Test-Path $chrome) -or (Test-Path $chrome86)) `
    "install Google Chrome, or set browser.channel to msedge in config.yaml"

# 5. The logged-in profile. Present but signed out is the case preflight cannot
#    see from here, so this only checks the directory exists at all.
$profileDir = Join-Path $env:USERPROFILE '.feed-engine\chrome-profile'
Test-Ok "chrome profile created" (Test-Path $profileDir) `
    "sign in once:  .\bin\feed-engine.exe -login"

# 6. Config, and whether the bank push is on. Not a failure either way: the
#    engine banks locally regardless, so this is worth reporting, not blocking.
$config = Join-Path $Root 'config.yaml'
Test-Ok "config.yaml present" (Test-Path $config) "config.yaml is missing from the repo root"

$local = Join-Path $Root 'config.local.yaml'
if (Test-Path $local) {
    $text = Get-Content $local -Raw
    if ($text -match 'enabled:\s*true') {
        if (($text -match 'token:\s*"fr_') -or ($env:FEED_ENGINE_BANK_TOKEN)) {
            Write-Host "  [ok]   bank push on, token present"
        } else {
            Write-Host "  [WARN] bank push is on but no token found" -ForegroundColor Yellow
            $notes += "set bank.token in config.local.yaml (a session token from the app), or export FEED_ENGINE_BANK_TOKEN. The engine refuses to start otherwise."
        }
    } else {
        Write-Host "  [note] bank push is off; seeds stay in local SQLite only"
        $notes += "to push seeds to the app, set bank.enabled true in config.local.yaml"
    }
} else {
    Write-Host "  [note] no config.local.yaml; bank push is off"
    $notes += "copy the bank/login blocks from config.yaml into config.local.yaml (gitignored) to turn the push on"
}

# 7. Whether the scheduled task already exists, and when it last ran.
$task = Get-ScheduledTask -TaskName 'feed-engine' -ErrorAction SilentlyContinue
if ($task) {
    $info = Get-ScheduledTaskInfo -TaskName 'feed-engine'
    Write-Host ("  [ok]   scheduled task registered (" + $task.State + ")")
    Write-Host ("         last run:  " + $info.LastRunTime + "  result: " + $info.LastTaskResult)
    Write-Host ("         next run:  " + $info.NextRunTime)
} else {
    Write-Host "  [note] scheduled task not registered"
    $notes += "register it:  .\scripts\feed-engine-enable.cmd"
}

Write-Host ""
if ($problems.Count -gt 0) {
    Write-Host "Fix these first:" -ForegroundColor Yellow
    foreach ($p in $problems) { Write-Host ("  - " + $p) }
    Write-Host ""
}
if ($notes.Count -gt 0) {
    Write-Host "Worth knowing:"
    foreach ($n in $notes) { Write-Host ("  - " + $n) }
    Write-Host ""
}
if ($problems.Count -eq 0) {
    Write-Host "Ready." -ForegroundColor Green
    Write-Host ""
}

if ($Strict -and $problems.Count -gt 0) { exit 1 }
exit 0
