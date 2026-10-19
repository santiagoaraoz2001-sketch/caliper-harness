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
# TargetDirectory is the application install root, not a replacement for the
# user's central data directory. They coincide on default Windows installs, so
# never rename or replace the root: workspaces, settings, current.json, updater
# versions, and legacy application files must stay where existing users expect.
$commands = Join-Path $target 'commands'
$launcher = Join-Path $commands 'caliper.cmd'
$marker = 'rem Caliper installed launcher'
if (Test-Path -LiteralPath $launcher) {
    if (-not (Test-Path -LiteralPath $launcher -PathType Leaf) -or
        -not ((Get-Content -LiteralPath $launcher) -contains $marker)) {
        throw "Refusing to replace an unrelated command: $launcher. Choose -TargetDirectory."
    }
}
foreach ($extension in @('exe', 'com', 'bat', 'ps1')) {
    if (Test-Path -LiteralPath (Join-Path $commands "caliper.$extension")) {
        throw "Another caliper command already exists in $commands. Choose -TargetDirectory."
    }
}
$app = $source
$relativeApp = '..'
if ($source.TrimEnd('\') -ine $target.TrimEnd('\')) {
    if ($target.StartsWith($source.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Install directory must not be inside the source bundle.'
    }
    $copyID = [guid]::NewGuid().ToString('N')
    $relativeApp = "..\applications\$copyID\Caliper"
    $app = Join-Path $target "applications\$copyID\Caliper"
    New-Item -ItemType Directory -Path $app -Force | Out-Null
    try {
        Get-ChildItem -LiteralPath $source -Force | ForEach-Object {
            Copy-Item -LiteralPath $_.FullName -Destination $app -Recurse -Force
        }
    } catch {
        throw "Copy failed; existing installation and work are unchanged. Partial files retained at $app. $_"
    }
}
# A separate command directory takes precedence over a legacy root caliper.exe.
# Keep the legacy binary/shim untouched. Relative paths also preserve spaces and
# Unicode user names without embedding them into the CMD file's text encoding.
New-Item -ItemType Directory -Path $commands -Force | Out-Null
$shim = "@echo off`r`n$marker`r`nsetlocal EnableExtensions DisableDelayedExpansion`r`nset `"PATH=%~dp0$relativeApp\bin;%PATH%`"`r`n`"%~dp0$relativeApp\$binary`" %*`r`nexit /b %errorlevel%`r`n"
$temporaryLauncher = Join-Path $commands ('.caliper-install-' + [guid]::NewGuid().ToString('N'))
[IO.File]::WriteAllText($temporaryLauncher, $shim, (New-Object System.Text.ASCIIEncoding))
try {
    if (Test-Path -LiteralPath $launcher) {
        # PowerShell converts $null to an empty string for this .NET overload.
        # Use a real backup path, retaining the previous launcher for recovery.
        $previousLauncher = Join-Path $commands ('.caliper-previous-' + [guid]::NewGuid().ToString('N') + '.cmd')
        [IO.File]::Replace($temporaryLauncher, $launcher, $previousLauncher)
    } else {
        [IO.File]::Move($temporaryLauncher, $launcher)
    }
} catch {
    throw "Launcher activation failed; existing launcher and work are unchanged. Application retained at $app. $_"
}
if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $commands.TrimEnd('\') })
    [Environment]::SetEnvironmentVariable('Path', ((@($commands) + $entries) -join ';'), 'User')
    # This PowerShell process can launch immediately. New shells inherit the
    # persisted user PATH after their terminal host refreshes its environment.
    $currentEntries = @($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ine $commands.TrimEnd('\') })
    $env:Path = (@($commands) + $currentEntries) -join ';'
}
Write-Output "Installed Caliper: $app"
Write-Output "Command: $launcher"
if ($NoPath) { Write-Output "Add $commands to PATH, then run caliper from your project directory." }
else { Write-Output 'Run caliper from your project directory. Reopen your terminal application if an existing terminal cannot find it.' }
