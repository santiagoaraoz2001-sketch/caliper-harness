# Preserve the caller's project cwd. Arguments are passed as an array.
$ErrorActionPreference = 'Stop'
if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'BUILD-INCOMPLETE')) { throw 'Incomplete application folder; see notices.' }
$exe = Join-Path $PSScriptRoot 'caliper-harness.exe'
if (-not (Test-Path -LiteralPath $exe) -or -not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'kuzu_shared.dll'))) { throw 'Keep the executable and native library in the intact application folder.' }
$env:PATH = (Join-Path $PSScriptRoot 'bin') + ';' + $env:PATH
& $exe tui @args
exit $LASTEXITCODE
