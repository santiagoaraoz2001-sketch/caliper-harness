# Native Windows installer regression tests; no models, network or Pester needed.
# Uses Windows cmd.exe as an inert argv/exit-code fixture, never a Caliper runtime.
#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'These installer tests require Windows.' }
& (Join-Path $PSScriptRoot 'ensure-windows-runtime.ps1') -CheckOnly
if ($LASTEXITCODE -ne 0) { throw 'Install the system runtime before running installer fixtures; tests never install prerequisites.' }
$installer = Join-Path $PSScriptRoot 'install-windows.ps1'
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('caliper-installer-test-' + [guid]::NewGuid().ToString('N'))
$originalLocalAppData = $env:LOCALAPPDATA
$originalPath = $env:PATH
$originalUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$originalLocation = Get-Location
function Assert($condition, [string]$message) {
    if (-not $condition) { throw "ASSERT: $message" }
}
function Write-FixtureFile([string]$path, [string]$text) {
    New-Item -ItemType Directory -Path (Split-Path -Parent $path) -Force | Out-Null
    [IO.File]::WriteAllText($path, $text)
}
function New-FixtureBundle([string]$path, [bool]$managed = $true) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
    Copy-Item -LiteralPath $installer -Destination (Join-Path $path 'install-windows.ps1')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'ensure-windows-runtime.ps1') -Destination $path
    $name = if ($managed) { 'caliper.exe' } else { 'caliper-harness.exe' }
    Copy-Item -LiteralPath $env:ComSpec -Destination (Join-Path $path $name)
    Write-FixtureFile (Join-Path $path 'kuzu_shared.dll') 'inert DLL fixture'
    Write-FixtureFile (Join-Path $path 'prompts.yaml') 'inert prompt fixture'
    if ($managed) { Write-FixtureFile (Join-Path $path 'runtime\manifest.json') '{"format":1}' }
}
try {
    New-Item -ItemType Directory -Path $fixture | Out-Null
    $env:LOCALAPPDATA = Join-Path $fixture ("User's data " + [char]0x00E9)
    $target = Join-Path $env:LOCALAPPDATA 'Caliper'
    $source = Join-Path $fixture 'Downloaded App'
    New-FixtureBundle $source
    $sentinels = @('workspaces\project\graph.db', 'workspaces\project\sessions\old.json',
        'settings.yaml', 'connections.yaml', 'current.json', 'versions\0.1.1\kept.txt',
        'caliper.exe', 'caliper.cmd', 'install-windows.ps1')
    foreach ($name in $sentinels) { Write-FixtureFile (Join-Path $target $name) "preserve $name" }
    # Default destination equals central data, including Windows case folding.
    & (Join-Path $source 'install-windows.ps1') -NoPath
    $launcher = Join-Path $target 'commands\caliper.cmd'
    $firstLauncher = [IO.File]::ReadAllText($launcher)
    foreach ($name in $sentinels) {
        Assert ([IO.File]::ReadAllText((Join-Path $target $name)) -eq "preserve $name") "default install moved or replaced $name"
    }
    Assert ((Get-ChildItem (Join-Path $target 'applications') -Directory).Count -eq 1) 'first immutable application copy missing'
    Assert ([Environment]::GetEnvironmentVariable('Path', 'User') -eq $originalUserPath) '-NoPath changed persisted PATH'
    # Reinstall is isolated too. Registration in persisted PATH is not qualified
    # by this fixture; command precedence below uses only this process's PATH.
    & (Join-Path $source 'install-windows.ps1') -NoPath
    Assert ((Get-ChildItem (Join-Path $target 'applications') -Directory).Count -eq 2) 'reinstall replaced an existing application copy'
    Assert ([IO.File]::ReadAllText($launcher) -ne $firstLauncher) 'launcher did not select new immutable copy'
    $backups = @(Get-ChildItem -LiteralPath (Join-Path $target 'commands') -Filter '.caliper-previous-*.cmd' -Force)
    Assert ($backups.Count -eq 1 -and [IO.File]::ReadAllText($backups[0].FullName) -eq $firstLauncher) 'atomic replacement did not preserve the prior launcher'
    $env:PATH = (Join-Path $target 'commands') + ';' + $target + ';' + $originalPath
    Assert (($env:PATH -split ';')[0] -eq (Join-Path $target 'commands')) 'command directory is not first on PATH'
    Assert ((Get-Command caliper).Source -eq $launcher) 'legacy executable shadows new launcher'
    foreach ($name in $sentinels) {
        Assert ([IO.File]::ReadAllText((Join-Path $target $name)) -eq "preserve $name") "reinstall changed $name"
    }
    $project = Join-Path $fixture 'Project With Spaces'
    New-Item -ItemType Directory -Path $project | Out-Null
    Set-Location $project
    $cwd = & $launcher /d /c cd
    Assert ($LASTEXITCODE -eq 0 -and $cwd.Trim() -eq $project) 'launcher changed caller working directory'
    & $launcher /d /c exit 7
    Assert ($LASTEXITCODE -eq 7) 'launcher lost child exit code'
    Set-Location $originalLocation
    $activeLauncher = [IO.File]::ReadAllText($launcher)
    # A failed prerequisite must never activate a candidate or change work.
    $sourceCheck = Join-Path $source 'ensure-windows-runtime.ps1'
    [IO.File]::WriteAllText($sourceCheck, 'exit 1')
    $beforeCopies = @(Get-ChildItem (Join-Path $target 'applications') -Directory).Count
    $refused = $false
    try { & (Join-Path $source 'install-windows.ps1') -NoPath } catch { $refused = $true }
    Assert $refused 'failed runtime prerequisite was ignored'
    Assert ([IO.File]::ReadAllText($launcher) -eq $activeLauncher) 'runtime failure changed active launcher'
    Assert (@(Get-ChildItem (Join-Path $target 'applications') -Directory).Count -eq $beforeCopies) 'runtime failure copied a candidate'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'ensure-windows-runtime.ps1') -Destination $sourceCheck -Force
    Write-FixtureFile (Join-Path $source 'BUILD-INCOMPLETE') 'incomplete'
    $refused = $false
    try { & (Join-Path $source 'install-windows.ps1') -NoPath } catch { $refused = $true }
    Assert $refused 'incomplete copy was installed'
    Assert ([IO.File]::ReadAllText($launcher) -eq $activeLauncher) 'incomplete bundle changed selection'
    Remove-Item -LiteralPath (Join-Path $source 'BUILD-INCOMPLETE')
    # Real copy failure preserves the prior launcher and every data sentinel.
    $locked = [IO.File]::Open((Join-Path $source 'prompts.yaml'), 'Open', 'Read', 'None')
    try {
        $refused = $false
        try { & (Join-Path $source 'install-windows.ps1') -NoPath } catch { $refused = $true }
        Assert $refused 'locked source file did not fail copy'
    } finally { $locked.Dispose() }
    Assert ([IO.File]::ReadAllText($launcher) -eq $activeLauncher) 'failed copy changed active launcher'
    foreach ($name in $sentinels) {
        Assert ([IO.File]::ReadAllText((Join-Path $target $name)) -eq "preserve $name") "failed copy changed $name"
    }
    # Explicit custom root is only an application location. Central data stays.
    $custom = Join-Path $fixture 'Custom Install'
    & (Join-Path $source 'install-windows.ps1') -TargetDirectory $custom -NoPath
    Assert (Test-Path (Join-Path $custom 'commands\caliper.cmd')) 'custom root ignored'
    Assert ([IO.File]::ReadAllText($launcher) -eq $activeLauncher) 'custom install changed default root launcher'
    # Running an already-installed legacy root registers it in place, avoiding
    # recursive copying of its workspaces/application children into themselves.
    $legacy = Join-Path $fixture 'Legacy Root'
    New-FixtureBundle $legacy $false
    Write-FixtureFile (Join-Path $legacy 'workspaces\keep') 'legacy work'
    & (Join-Path $legacy 'install-windows.ps1') -TargetDirectory $legacy -NoPath
    Assert (-not (Test-Path (Join-Path $legacy 'applications'))) 'legacy in-place registration copied its own data'
    Assert ([IO.File]::ReadAllText((Join-Path $legacy 'workspaces\keep')) -eq 'legacy work') 'legacy work changed'
    & (Join-Path $legacy 'commands\caliper.cmd') /d /c exit 6
    Assert ($LASTEXITCODE -eq 6) 'offline legacy launcher did not work'
    # Do not overwrite an unrelated command in an explicitly selected root.
    $unrelated = Join-Path $fixture 'Unrelated'
    Write-FixtureFile (Join-Path $unrelated 'commands\caliper.cmd') 'unrelated'
    $refused = $false
    try { & (Join-Path $source 'install-windows.ps1') -TargetDirectory $unrelated -NoPath } catch { $refused = $true }
    Assert $refused 'unrelated command overwritten'
    Assert ([IO.File]::ReadAllText((Join-Path $unrelated 'commands\caliper.cmd')) -eq 'unrelated') 'unrelated command modified'
    Assert ([Environment]::GetEnvironmentVariable('Path', 'User') -eq $originalUserPath) 'fixture changed persisted User PATH'
    Write-Output 'UNQUALIFIED: persisted PATH registration and new-terminal discovery (no User PATH writes)'
    $global:LASTEXITCODE = 0
    Write-Output 'PASS: default/custom/legacy install, reinstall, data preservation, copy failure, command precedence, cwd and exit code'
} finally {
    Set-Location $originalLocation
    $userPathUnchanged = [Environment]::GetEnvironmentVariable('Path', 'User') -eq $originalUserPath
    $env:PATH = $originalPath
    $env:LOCALAPPDATA = $originalLocalAppData
    $resolvedFixture = [IO.Path]::GetFullPath($fixture)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    Assert ($resolvedFixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) 'fixture cleanup escaped TEMP'
    Remove-Item -LiteralPath $resolvedFixture -Recurse -Force
    Assert $userPathUnchanged 'fixture changed persisted User PATH; it was not restored or overwritten'
}
