# Registers feed-engine with Windows Task Scheduler: a few runs a day, at
# deliberately odd minutes, each one adding its own random delay on top.
#
#   .\scripts\install-task.ps1
#   .\scripts\install-task.ps1 -Times 08:12,19:37
#   .\scripts\install-task.ps1 -Uninstall
#
# LogonType Interactive is deliberate: the run drives a headed Chrome window,
# so it only makes sense while you are actually logged in. It will not wake the
# machine and it will not run on the lock screen.

[CmdletBinding()]
param(
    [string[]] $Times = @('09:17', '14:43', '21:09'),
    [string]   $TaskName = 'feed-engine',
    [switch]   $Uninstall
)

$ErrorActionPreference = 'Stop'

$Root   = Split-Path -Parent $PSScriptRoot
$Runner = Join-Path $PSScriptRoot 'run.ps1'

if ($Uninstall) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Host "removed scheduled task '$TaskName'"
    exit 0
}

if (-not (Test-Path (Join-Path $Root 'bin\feed-engine.exe'))) {
    Write-Error "build it first: go build -o bin\feed-engine.exe .\cmd\feed-engine"
    exit 1
}

$action = New-ScheduledTaskAction `
    -Execute 'powershell.exe' `
    -Argument "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$Runner`"" `
    -WorkingDirectory $Root

$triggers = foreach ($t in $Times) { New-ScheduledTaskTrigger -Daily -At $t }

# StartWhenAvailable picks up a run missed because the laptop was shut; the hour
# limit is a backstop in case a browser session ever wedges.
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -DontStopIfGoingOnBatteries `
    -AllowStartIfOnBatteries `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit (New-TimeSpan -Hours 1)

$principal = New-ScheduledTaskPrincipal `
    -UserId "$env:USERDOMAIN\$env:USERNAME" `
    -LogonType Interactive `
    -RunLevel Limited

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $triggers `
    -Settings $settings `
    -Principal $principal `
    -Description 'Read-only X List idea harvester' `
    -Force | Out-Null

Write-Host "registered '$TaskName' at: $($Times -join ', ')"
Write-Host "run it now:   Start-ScheduledTask -TaskName $TaskName"
Write-Host "check it:     Get-ScheduledTaskInfo -TaskName $TaskName"
Write-Host "remove it:    .\scripts\install-task.ps1 -Uninstall"
