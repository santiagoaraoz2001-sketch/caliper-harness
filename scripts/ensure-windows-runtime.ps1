# Check the system prerequisite before loading Caliper's native executable.
# Microsoft installs its own runtime; Caliper does not redistribute its DLLs.
#Requires -Version 5.1
param([switch]$CheckOnly, [switch]$Json)
$ErrorActionPreference = 'Stop'
# A PowerShell 7 parent exports its own PSModulePath to children. Windows
# PowerShell 5.1 then fails to autoload Core-edition modules such as
# Microsoft.PowerShell.Security (Get-AuthenticodeSignature). Restore the
# Windows PowerShell defaults for this process before any cmdlet autoloads.
if ($PSEdition -eq 'Desktop') {
    $env:PSModulePath = "$env:USERPROFILE\Documents\WindowsPowerShell\Modules;$env:ProgramFiles\WindowsPowerShell\Modules;$env:SystemRoot\system32\WindowsPowerShell\v1.0\Modules"
}
$CaliperVCMinimum = [version]'14.44.35211.0'
$CaliperVCFiles = @('msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll')

function Get-CaliperRuntimeFile([string]$Path) {
    # Only FileNotFound/DirectoryNotFound mean absent. Access and I/O errors
    # must not trigger an installation that cannot fix the verification error.
    try { $item = Get-Item -LiteralPath $Path -ErrorAction Stop }
    catch [System.Management.Automation.ItemNotFoundException] { return $null }
    if ($item.PSIsContainer) { throw "Runtime DLL path is a directory: $Path" }
    $stream = [IO.File]::OpenRead($Path)
    try {
        $reader = New-Object IO.BinaryReader($stream)
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5a4d) { throw "Invalid runtime PE file: $Path" }
        $stream.Position = 0x3c
        $offset = $reader.ReadUInt32()
        if ([long]$offset + 6 -gt $stream.Length) { throw "Invalid runtime PE header: $Path" }
        $stream.Position = $offset
        if ($reader.ReadUInt32() -ne 0x4550 -or $reader.ReadUInt16() -ne 0x8664) { throw "Runtime DLL is not an x64 PE file: $Path" }
    } finally { $stream.Dispose() }
    $v = [Diagnostics.FileVersionInfo]::GetVersionInfo($Path)
    if (-not $v.FileVersion -or $v.FileMajorPart -eq 0) { throw "Runtime DLL has no readable version information: $Path" }
    return [version]("{0}.{1}.{2}.{3}" -f $v.FileMajorPart, $v.FileMinorPart, $v.FileBuildPart, $v.FilePrivatePart)
}

function Get-CaliperRuntimeStatus {
    $files = @()
    foreach ($name in $CaliperVCFiles) {
        $path = Join-Path $env:SystemRoot "System32\$name"
        $version = Get-CaliperRuntimeFile $path
        $files += [pscustomobject]@{name=$name; path=$path; version=[string]$version; ready=($null -ne $version -and $version -ge $CaliperVCMinimum)}
    }
    return [pscustomobject]@{minimum=[string]$CaliperVCMinimum; architecture='x64'; ready=(@($files | Where-Object { -not $_.ready }).Count -eq 0); files=$files}
}

function Assert-CaliperInstallerSignature([string]$Path) {
    try { $signature = Get-AuthenticodeSignature -LiteralPath $Path }
    catch [System.Management.Automation.CommandNotFoundException] {
        throw 'Windows could not load the Authenticode signature checker (Microsoft.PowerShell.Security), so the Microsoft runtime download cannot be verified. Nothing was installed. Retry from a fresh Windows PowerShell or Command Prompt window.'
    }
    if ($signature.Status -ne 'Valid' -or $null -eq $signature.SignerCertificate -or
        $signature.SignerCertificate.Subject -notmatch '(^|,\s*)CN=Microsoft Corporation(,|$)') {
        throw 'Microsoft runtime download did not have a valid Microsoft Authenticode signature. Nothing was installed.'
    }
}

function Install-CaliperSystemRuntime {
    $temporary = Join-Path ([IO.Path]::GetTempPath()) ('caliper-vc-runtime-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temporary | Out-Null
    $installer = Join-Path $temporary 'vc_redist.x64.exe'
    $tls = [Net.ServicePointManager]::SecurityProtocol
    try {
        [Net.ServicePointManager]::SecurityProtocol = $tls -bor [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -UseBasicParsing -Uri 'https://aka.ms/vc14/vc_redist.x64.exe' -OutFile $installer
        Assert-CaliperInstallerSignature $installer
        Write-Host 'Opening Microsoft Visual C++ Runtime setup. Windows may ask for administrator approval; review Microsoft''s terms in its installer.'
        $process = Start-Process -FilePath $installer -ArgumentList '/install', '/norestart' -Verb RunAs -PassThru -Wait
        return $process.ExitCode
    } catch {
        if ($_.Exception.NativeErrorCode -eq 1223) { throw 'Microsoft runtime setup was cancelled at the Windows approval prompt. Caliper was not activated.' }
        throw
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $tls
        Remove-Item -LiteralPath $temporary -Recurse -Force
    }
}

function Ensure-CaliperWindowsRuntime([switch]$CheckOnly, [switch]$Json) {
    if ($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitProcess) { throw 'Run this check from 64-bit Windows PowerShell.' }
    $status = Get-CaliperRuntimeStatus
    if (-not $status.ready) {
        $needed = @($status.files | Where-Object { -not $_.ready } | ForEach-Object {
            if ($_.version) { "$($_.name) is outdated ($($_.version))" } else { "$($_.name) is missing" }
        }) -join '; '
        if ($CheckOnly) { throw "Microsoft Visual C++ x64 Runtime $CaliperVCMinimum or newer is required. $needed. Run ensure-windows-runtime.ps1 interactively to install it." }
        Write-Host "Caliper needs Microsoft Visual C++ x64 Runtime $CaliperVCMinimum or newer. $needed. Downloading Microsoft's installer; Caliper files and work stay unchanged."
        $code = Install-CaliperSystemRuntime
        if ($code -eq 3010 -or $code -eq 1641) {
            Write-Host 'Microsoft runtime setup requires a Windows restart. Restart Windows, then run Caliper again. Caliper was not activated.'
            return 3010
        }
        if ($code -eq 1602) { throw 'Microsoft runtime setup was cancelled. Caliper was not activated.' }
        if ($code -ne 0) { throw "Microsoft runtime setup failed (exit $code). Caliper was not activated." }
        $status = Get-CaliperRuntimeStatus
        if (-not $status.ready) { throw 'Microsoft setup finished, but required x64 runtime files are still missing or too old. Restart Windows or repair the runtime through Microsoft setup; Caliper was not activated.' }
    }
    if ($Json) { $status | ConvertTo-Json -Depth 4 }
    return 0
}

# Dot-sourcing exposes the same functions to isolated tests without running setup.
if ($MyInvocation.InvocationName -ne '.') {
    try {
        $result = @(Ensure-CaliperWindowsRuntime -CheckOnly:$CheckOnly -Json:$Json)
        $code = [int]$result[-1]
        if ($Json -and $result.Count -gt 1) { $result[0..($result.Count - 2)] }
        exit $code
    } catch {
        Write-Error -Message $_.Exception.Message -ErrorAction Continue
        exit 1
    }
}
