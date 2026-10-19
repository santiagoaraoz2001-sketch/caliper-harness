# Isolated prerequisite control-flow tests. No network, installer or system changes.
#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'ensure-windows-runtime.ps1')
function Assert($condition, [string]$message) { if (-not $condition) { throw "ASSERT: $message" } }
function Assert-Throws([scriptblock]$action, [string]$pattern) {
    $caught = $false
    try { & $action | Out-Null } catch { $caught = $_.Exception.Message -match $pattern }
    Assert $caught "expected error matching $pattern"
}
$originalInstall = ${function:Install-CaliperSystemRuntime}
$originalOS = $env:OS
$originalRoot = $env:SystemRoot
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('caliper-crt-test-' + [guid]::NewGuid().ToString('N'))
try {
    New-Item -ItemType Directory -Path $fixture | Out-Null
    # The real file reader distinguishes absent from invalid and wrong-arch PE.
    Assert ($null -eq (Get-CaliperRuntimeFile (Join-Path $fixture 'absent.dll'))) 'missing file must be absent'
    $dll = Join-Path $fixture 'bad.dll'
    [IO.File]::WriteAllText($dll, 'not a PE')
    Assert-Throws { Get-CaliperRuntimeFile $dll } 'Invalid runtime PE'
    $bytes = New-Object byte[] 128
    $bytes[0] = 0x4d; $bytes[1] = 0x5a; $bytes[60] = 64
    $bytes[64] = 0x50; $bytes[65] = 0x45; $bytes[68] = 0x4c; $bytes[69] = 0x01
    [IO.File]::WriteAllBytes($dll, $bytes)
    Assert-Throws { Get-CaliperRuntimeFile $dll } 'not an x64'

    # Mock system reads; retain production aggregation and version comparison.
    $env:SystemRoot = $fixture
    function Get-CaliperRuntimeFile([string]$Path) { return $script:fileVersion }
    $script:fileVersion = [version]'14.44.35211.0'
    Assert (Get-CaliperRuntimeStatus).ready 'minimum supported runtime refused'
    $script:fileVersion = [version]'14.50.40000.0'
    Assert (Get-CaliperRuntimeStatus).ready 'newer cumulative runtime refused'
    $script:fileVersion = [version]'14.40.30000.0'
    Assert (-not (Get-CaliperRuntimeStatus).ready) 'outdated runtime accepted'
    $script:fileVersion = $null
    Assert (-not (Get-CaliperRuntimeStatus).ready) 'absent runtime accepted'

    $env:OS = 'Windows_NT'
    $script:installed = 0
    $script:ready = $true
    $script:installCode = 0
    $script:afterInstall = $true
    function Get-CaliperRuntimeStatus {
        if ($script:checkError) { throw 'verification access denied' }
        return [pscustomobject]@{ready=$script:ready; files=@(); minimum='14.44.35211.0'}
    }
    function Install-CaliperSystemRuntime {
        $script:installed++
        $script:ready = $script:afterInstall
        return $script:installCode
    }
    $output = @(Ensure-CaliperWindowsRuntime 6>&1)
    Assert ($output.Count -eq 1 -and $output[0] -eq 0 -and $script:installed -eq 0) 'installed path must be silent and never download/install'
    $script:checkError = $true
    Assert-Throws { Ensure-CaliperWindowsRuntime } 'verification access denied'
    Assert ($script:installed -eq 0) 'verification error launched installer'
    $script:checkError = $false
    $script:ready = $false
    Assert-Throws { Ensure-CaliperWindowsRuntime -CheckOnly } 'required'
    Assert ($script:installed -eq 0) 'CheckOnly launched installer'
    Assert ((Ensure-CaliperWindowsRuntime) -eq 0) 'successful setup not rechecked'
    Assert ($script:installed -eq 1 -and $script:ready) 'missing runtime was not installed/rechecked'
    foreach ($code in @(3010, 1641)) {
        $script:ready=$false; $script:installCode=$code
        Assert ((Ensure-CaliperWindowsRuntime) -eq 3010) 'reboot must block activation'
    }
    $script:ready=$false; $script:installCode=1602
    Assert-Throws { Ensure-CaliperWindowsRuntime } 'cancelled'
    $script:ready=$false; $script:installCode=123
    Assert-Throws { Ensure-CaliperWindowsRuntime } 'exit 123'
    $script:ready=$false; $script:installCode=0; $script:afterInstall=$false
    Assert-Throws { Ensure-CaliperWindowsRuntime } 'still missing or too old'

    # Signature validation is mandatory; a valid non-Microsoft signer is refused.
    function Get-AuthenticodeSignature { return $script:signature }
    $script:signature=[pscustomobject]@{Status='NotSigned'; SignerCertificate=$null}
    Assert-Throws { Assert-CaliperInstallerSignature 'fixture' } 'valid Microsoft'
    $script:signature=[pscustomobject]@{Status='Valid'; SignerCertificate=[pscustomobject]@{Subject='CN=Other Corporation'}}
    Assert-Throws { Assert-CaliperInstallerSignature 'fixture' } 'valid Microsoft'
    $script:signature.SignerCertificate.Subject='CN=Microsoft Corporation, O=Microsoft Corporation, C=US'
    Assert-CaliperInstallerSignature 'fixture'
    # Exercise actual acquisition order with isolated process/network doubles.
    ${function:Install-CaliperSystemRuntime} = $originalInstall
    $script:opened = 0
    $script:downloadPath = ''
    function Invoke-WebRequest {
        param($Uri, $OutFile, [switch]$UseBasicParsing)
        Assert ($Uri -eq 'https://aka.ms/vc14/vc_redist.x64.exe') 'unexpected runtime download source'
        $script:downloadPath = $OutFile
        [IO.File]::WriteAllText($OutFile, 'fixture')
    }
    function Start-Process {
        param($FilePath, $ArgumentList, $Verb, [switch]$PassThru, [switch]$Wait)
        Assert ($Verb -eq 'RunAs' -and ($ArgumentList -join ' ') -eq '/install /norestart') 'installer must show official UI with UAC and no forced reboot'
        $script:opened++
        return [pscustomobject]@{ExitCode=0}
    }
    $script:signature.Status='NotSigned'
    Assert-Throws { Install-CaliperSystemRuntime } 'valid Microsoft'
    Assert ($script:opened -eq 0 -and -not (Test-Path -LiteralPath $script:downloadPath)) 'unsigned download executed or retained'
    $script:signature.Status='Valid'
    Assert ((Install-CaliperSystemRuntime) -eq 0) 'signed installer did not run'
    Assert ($script:opened -eq 1 -and -not (Test-Path -LiteralPath $script:downloadPath)) 'signed temporary installer not cleaned up'
    # Execute the actual builder dispatch against an argument recorder. The
    # independent downloaded interpreter must normalize the target, never itself.
    & {
        $errors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile(
            (Join-Path $PSScriptRoot 'prepare-windows-runtime.ps1'), [ref]$null, [ref]$errors)
        Assert (-not $errors) 'builder parse failed'
        $calls = @($ast.FindAll({ param($node)
            $node -is [System.Management.Automation.Language.CommandAst] -and
            $node.Extent.Text -match '^& .*use_system_runtime\.py'
        }, $true))
        Assert ($calls.Count -eq 1) 'expected one normalization dispatch'
        $cleanup = @($ast.FindAll({ param($node)
            $node -is [System.Management.Automation.Language.CommandAst] -and
            $node.GetCommandName() -eq 'Remove-Item' -and $node.Extent.Text -match '\$stage'
        }, $true))
        $verify = @($ast.FindAll({ param($node)
            $node -is [System.Management.Automation.Language.CommandAst] -and
            $node.Extent.Text -match '^& .*verify_runtime\.py'
        }, $true))
        Assert ($cleanup.Count -eq 1 -and $verify.Count -eq 1 -and
            $calls[0].Extent.EndOffset -lt $cleanup[0].Extent.StartOffset -and
            $cleanup[0].Extent.EndOffset -lt $verify[0].Extent.StartOffset) 'builder staging must be removed after normalization and before product verification'
        $laterStageReads = @($ast.FindAll({ param($node)
            $node -is [System.Management.Automation.Language.VariableExpressionAst] -and
            $node.VariablePath.UserPath -in @('stage', 'base', 'rgStage', 'rgZip', 'pyArchive') -and
            $node.Extent.StartOffset -gt $cleanup[0].Extent.EndOffset
        }, $true))
        Assert ($laterStageReads.Count -eq 0) 'builder uses staging after cleanup'
        $base = 'downloaded-source-python'
        $decisionDir = 'new-private-runtime'
        $App = 'application'
        $script:normalizerInterpreter = ''
        $script:normalizerArguments = @()
        function Join-Path($Path, $ChildPath) {
            if ($ChildPath -eq 'python.exe') {
                $script:normalizerInterpreter = $Path
                return 'Invoke-InterpreterFixture'
            }
            return "$Path/$ChildPath"
        }
        function Invoke-InterpreterFixture {
            $script:normalizerArguments = @($args)
        }
        Invoke-Expression $calls[0].Extent.Text
        Assert ($script:normalizerInterpreter -eq $base) 'normalizer loads the runtime it must modify'
        Assert ($script:normalizerArguments.Count -eq 3 -and
            $script:normalizerArguments[0] -eq '-I' -and
            $script:normalizerArguments[2] -eq $decisionDir) 'normalizer arguments lost isolated mode or staged target'
    }
    $global:LASTEXITCODE = 0
    Write-Output 'PASS: missing/invalid PE, minimum/newer/outdated runtime, silent installed path, verification errors, check-only, recheck, reboot/cancel/failure, Microsoft signature'
} finally {
    $env:OS=$originalOS; $env:SystemRoot=$originalRoot
    Remove-Item -LiteralPath $fixture -Recurse -Force
}
