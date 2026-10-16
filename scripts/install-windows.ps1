# Install this Caliper bundle folder and put `caliper` on your PATH.
# Run from inside the downloaded bundle folder:
#   powershell -ExecutionPolicy Bypass -File .\install-windows.ps1
# Then open a NEW terminal (cmd, PowerShell or Windows Terminal) and run:
#   caliper
#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
$source = $PSScriptRoot
if (-not (Test-Path (Join-Path $source 'caliper.exe')) -or -not (Test-Path (Join-Path $source 'runtime\manifest.json'))) {
    throw 'incomplete bundle: run this from the full Caliper folder (caliper.exe plus runtime\manifest.json) - do not copy the executable alone'
}
$target = Join-Path $env:LOCALAPPDATA 'Caliper'
if ([System.IO.Path]::GetFullPath($source) -ieq [System.IO.Path]::GetFullPath($target)) {
    Write-Output "Already in the install folder: $target"
} else {
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    try {
        Copy-Item (Join-Path $source '*') $target -Recurse -Force
    } catch {
        throw "could not copy into $target (close Caliper and try again): $_"
    }
}
if (-not (Test-Path (Join-Path $target 'caliper.exe'))) { throw "install incomplete: $($target)\caliper.exe missing" }

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$entries = @($userPath -split ';' | Where-Object { $_ })
if ($entries -inotcontains $target) {
    [Environment]::SetEnvironmentVariable('Path', (($entries + $target) -join ';'), 'User')
    Write-Output "Added $target to your PATH."
} else {
    Write-Output "$target is already on your PATH."
}
Write-Output ''
Write-Output 'Done. Open a NEW terminal (cmd, PowerShell or Windows Terminal) and run:'
Write-Output '  caliper'
Write-Output 'Existing terminals keep the old PATH and will not find it.'
