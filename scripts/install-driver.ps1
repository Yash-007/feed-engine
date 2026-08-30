# Installs the Playwright driver that playwright-go needs.
#
# Why this exists: playwright-go v0.5200.1 downloads its driver from
# playwright.azureedge.net (and two sibling mirrors). Those hosts were retired
# and now return 404, so the library's own auto-install can never succeed. The
# driver is just the playwright-core npm package plus a node binary in a fixed
# layout, and npm still serves both, so build the directory directly:
#
#   <cache>\ms-playwright-go\<version>\node.exe
#   <cache>\ms-playwright-go\<version>\package\cli.js
#
# Requires Node on PATH. Run once per machine:
#
#   .\scripts\install-driver.ps1
#
# Browsers are NOT downloaded here - config.yaml uses `channel: chrome`, which
# drives your installed Google Chrome rather than a Playwright-managed build.
#
# Keep this file pure ASCII: Windows PowerShell 5.1 reads UTF-8-without-BOM as
# cp1252, where an em dash's trailing byte decodes to a curly closing quote that
# PowerShell honours as a string delimiter, and the script stops parsing.

[CmdletBinding()]
param(
    # Must match playwrightCliVersion in the playwright-go release you build against.
    [string] $Version = '1.52.0'
)

$ErrorActionPreference = 'Stop'

if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    Write-Error "node is not on PATH - install Node.js first, it supplies both the driver package and node.exe"
    exit 1
}

$driver  = Join-Path $env:LOCALAPPDATA "ms-playwright-go\$Version"
$staging = Join-Path ([System.IO.Path]::GetTempPath()) "feed-engine-pw-$Version"

if (Test-Path $staging) { Remove-Item $staging -Recurse -Force }
New-Item -ItemType Directory -Path $staging | Out-Null

Write-Host "installing playwright-core@$Version from npm ..."
Push-Location $staging
try {
    # Routed through cmd deliberately: in Windows PowerShell 5.1, capturing npm's
    # stderr notices turns them into ErrorRecords and aborts an install that
    # actually succeeded.
    & cmd /c "npm install playwright-core@$Version --no-save --no-audit --no-fund --loglevel=error"
    if ($LASTEXITCODE -ne 0) { throw "npm install failed with exit code $LASTEXITCODE" }
}
finally { Pop-Location }

$pkg = Join-Path $staging 'node_modules\playwright-core'
if (-not (Test-Path (Join-Path $pkg 'cli.js'))) { throw "playwright-core/cli.js missing under $pkg" }

if (Test-Path $driver) { Remove-Item $driver -Recurse -Force }
New-Item -ItemType Directory -Path $driver | Out-Null

Copy-Item $pkg (Join-Path $driver 'package') -Recurse -Force
Copy-Item (Get-Command node).Source (Join-Path $driver 'node.exe') -Force
Remove-Item $staging -Recurse -Force

# Exactly the check playwright-go makes before deciding to download.
$reported = & (Join-Path $driver 'node.exe') (Join-Path $driver 'package\cli.js') --version
if ($reported -notmatch [regex]::Escape($Version)) {
    throw "driver reports '$reported', expected $Version"
}

Write-Host "driver ready at $driver ($reported)"
