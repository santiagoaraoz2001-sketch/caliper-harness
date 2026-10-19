# Install the intact bundle and register `caliper` without administrator rights.
#Requires -Version 5.1
param([string]$TargetDirectory = (Join-Path $env:LOCALAPPDATA 'Caliper'), [switch]$NoPath)
$ErrorActionPreference = 'Stop'
$source = [IO.Path]::GetFullPath($PSScriptRoot)
$target = [IO.Path]::GetFullPath($TargetDirectory)
if ($env:OS -ne 'Windows_NT') { throw 'Run this installer on Windows.' }
if (Test-Path -LiteralPath (Join-Path $source 'BUILD-INCOMPLETE')) { throw 'Cannot install an incomplete bundle.' }
$managed = Test-Path -LiteralPath (Join-Path $source 'caliper.exe')
$binary = if ($managed) { 'caliper.exe' } else { 'caliper-harness.exe' }
foreach ($required in @($binary, 'kuzu_shared.dll', 'prompts.yaml')) {
    if (-not (Test-Path -LiteralPath (Join-Path $source $required) -PathType Leaf)) {
        throw "Incomplete bundle: missing $required. Run the installer from the full application folder."
    }
}
if ($managed -and -not (Test-Path -LiteralPath (Join-Path $source 'runtime\manifest.json') -PathType Leaf)) {
    throw 'Incomplete managed bundle: runtime\manifest.json is missing.'
}
# Copy to a new sibling folder first. An interrupted copy cannot overwrite the
# installed runtime. Keep the previous install as a backup instead of deleting it.
if ($source.TrimEnd('\') -ine $target.TrimEnd('\')) {
    if ($target.StartsWith($source.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Install directory must not be inside the source bundle.'
    }
    if (Test-Path -LiteralPath $target) {
        if (-not (Test-Path -LiteralPath (Join-Path $target 'install-windows.ps1') -PathType Leaf)) {
            throw "Refusing to replace an unrelated folder: $target. Choose -TargetDirectory or move it yourself."
        }
    }
    $staging = $target + '.install-' + [guid]::NewGuid().ToString('N')
    New-Item -ItemType Directory -Path $staging -Force | Out-Null
    try {
        Get-ChildItem -LiteralPath $source -Force | ForEach-Object {
            Copy-Item -LiteralPath $_.FullName -Destination $staging -Recurse -Force
        }
    } catch {
        throw "Copy failed; existing installation is unchanged. Partial files retained at $staging. $_"
    }
    $backup = $null
    if (Test-Path -LiteralPath $target) {
        $backup = $target + '.previous-' + [guid]::NewGuid().ToString('N')
        Move-Item -LiteralPath $target -Destination $backup
    }
    try { Move-Item -LiteralPath $staging -Destination $target }
    catch {
        if ($backup) { Move-Item -LiteralPath $backup -Destination $target }
        throw "Install activation failed; previous folder restored. Close Caliper and retry. $_"
    }
    if ($backup) { Write-Output "Previous installation preserved: $backup" }
}
# The offline bundle uses the same binary with a longer filename. A CMD shim
# preserves its executable-relative assets, caller cwd, arguments and exit code.
if (-not $managed) {
    $shim = "@echo off`r`nsetlocal`r`nset `"PATH=%~dp0bin;%PATH%`"`r`n`"%~dp0caliper-harness.exe`" %*`r`nexit /b %errorlevel%`r`n"
    [IO.File]::WriteAllText((Join-Path $target 'caliper.cmd'), $shim, (New-Object System.Text.ASCIIEncoding))
}
if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $target.TrimEnd('\') })
    [Environment]::SetEnvironmentVariable('Path', ((@($target) + $entries) -join ';'), 'User')
    # This PowerShell process can launch immediately. New shells inherit the
    # persisted user PATH after their terminal host refreshes its environment.
    $currentEntries = @($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $target.TrimEnd('\') })
    $env:Path = (@($target) + $currentEntries) -join ';'
}
Write-Output "Installed Caliper: $target"
if ($NoPath) { Write-Output "Add $target to PATH, then run caliper from your project directory." }
else { Write-Output 'Run caliper from your project directory. Reopen your terminal application if an existing terminal cannot find it.' }
