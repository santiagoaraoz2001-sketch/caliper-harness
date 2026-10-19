# Builder-only dependency acquisition into a new private development bundle.
# Users receive the finished runtime; a healthy launch never runs uv/pip.
# Only a startup verification failure, with an explicit yes from the user,
# re-downloads these pinned dependencies (internal/bundle Repair).
#Requires -Version 5.1
param([Parameter(Mandatory = $true)][string]$App, [switch]$AllowDownloads)
$ErrorActionPreference = 'Stop'
if (-not $AllowDownloads) { throw 'Runtime downloads require -AllowDownloads.' }
if ($env:OS -ne 'Windows_NT' -or $env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'Windows x64 builder required' }
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$App = (Resolve-Path -LiteralPath $App).Path
if (Test-Path (Join-Path $App 'runtime')) { throw 'refusing existing runtime; use a new bundle output' }
# MSYS2 is on PATH for gcc/Bash. Its GNU tar treats a Windows drive colon
# as a remote host; archive extraction must use the native Windows bsdtar.
$nativeTar = Join-Path $env:SystemRoot 'System32\tar.exe'
if (-not (Test-Path -LiteralPath $nativeTar -PathType Leaf)) {
    throw "builder tool missing: native Windows tar ($nativeTar)"
}
foreach ($tool in 'uv', 'curl.exe') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "builder tool missing: $tool" }
}
$sources = Get-Content -Raw (Join-Path $root 'python\windows\sources.json') | ConvertFrom-Json

function Get-Pinned([string]$Url, [string]$Sha, [string]$Out) {
    if ($Url -notmatch '^https://' -or $Sha -notmatch '^[0-9a-fA-F]{64}$') { throw 'Invalid pinned source URL or SHA256.' }
    & curl.exe -fsSL --retry 2 -o $Out $Url
    if ($LASTEXITCODE -ne 0) { throw "download failed: $Url" }
    if ((Get-FileHash -Algorithm SHA256 $Out).Hash.ToLower() -ne $Sha.ToLower()) {
        throw "sha256 mismatch: $Out"
    }
}

$stage = Join-Path $App '.runtime-build'
New-Item -ItemType Directory -Path $stage | Out-Null
$env:UV_CACHE_DIR = if ($env:CALIPER_BUILD_CACHE) { $env:CALIPER_BUILD_CACHE } else { Join-Path $stage 'cache' }

$pyArchive = Join-Path $stage 'python.tar.gz'
Get-Pinned $sources.python.url $sources.python.sha256 $pyArchive
& $nativeTar -xzf $pyArchive -C $stage
if ($LASTEXITCODE -ne 0) { throw 'cannot extract standalone Python' }
$base = Join-Path $stage 'python'
if (-not (Test-Path (Join-Path $base 'python.exe'))) { throw 'standalone Python missing after download' }

New-Item -ItemType Directory -Force -Path (Join-Path $App 'runtime'), (Join-Path $App 'bin'), (Join-Path $App 'notices') | Out-Null
Copy-Item (Join-Path $root 'python\windows\sources.json') (Join-Path $App 'runtime\sources.json')

# The decision sidecar is the only private interpreter on Windows; the
# worker model is served by Ollama (external, like the MLX worker is not).
$decisionDir = Join-Path $App 'runtime\decision'
New-Item -ItemType Directory -Path $decisionDir | Out-Null
Copy-Item (Join-Path $base '*') $decisionDir -Recurse -Force
# uv marks its standalone base externally managed. This override applies
# only to the fresh copied interpreter under this owned output folder.
& uv --no-config pip sync --python (Join-Path $decisionDir 'python.exe') --system --break-system-packages --require-hashes --only-binary :all: --link-mode copy (Join-Path $root 'python\windows\decision.lock')
if ($LASTEXITCODE -ne 0) { throw 'uv pip sync failed for decision runtime' }
Copy-Item (Join-Path $root 'python\windows\decision.lock') (Join-Path $decisionDir 'requirements.lock')

$rgZip = Join-Path $stage 'ripgrep.zip'
Get-Pinned $sources.ripgrep.url $sources.ripgrep.sha256 $rgZip
$rgStage = Join-Path $stage 'ripgrep'
Expand-Archive -LiteralPath $rgZip -DestinationPath $rgStage -Force
$rgExe = Get-ChildItem -Recurse -Filter 'rg.exe' $rgStage | Select-Object -First 1
if (-not $rgExe) { throw 'rg.exe missing after ripgrep download' }
Copy-Item $rgExe.FullName (Join-Path $App 'bin\rg.exe')
Get-ChildItem -Recurse $rgStage -Include 'LICENSE*', 'UNLICENSE' | ForEach-Object {
    Copy-Item $_.FullName (Join-Path $App ("notices\ripgrep-" + $_.Name)) -ErrorAction SilentlyContinue
}

# Kuzu requires the VC runtime beside caliper.exe. The private interpreter's
# DLLs and a developer machine's System32 do not provide a portable app closure.
$vcArchive = Join-Path $stage 'vc-runtime.zip'
Get-Pinned $sources.vc_runtime.url $sources.vc_runtime.sha256 $vcArchive
$vcStage = Join-Path $stage 'vc-runtime'
Expand-Archive -LiteralPath $vcArchive -DestinationPath $vcStage
$vcDirectory = Join-Path $vcStage $sources.vc_runtime.archive_directory
foreach ($name in 'msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll') {
    $source = Join-Path $vcDirectory $name
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash.ToLower() -ne $sources.vc_runtime.files.$name.sha256) {
        throw "VC runtime file hash mismatch: $name"
    }
    Copy-Item -LiteralPath $source -Destination (Join-Path $App $name)
}
# PyTorch also imports MSVCP140 from the interpreter's application directory.
# Python already includes the same pinned VCRUNTIME files; supply its C++ peer.
Copy-Item -LiteralPath (Join-Path $App 'msvcp140.dll') -Destination (Join-Path $decisionDir 'msvcp140.dll')
Copy-Item -LiteralPath (Join-Path $root 'python\windows\VC-RUNTIME-NOTICE.txt') -Destination (Join-Path $App 'notices\Microsoft-VC-RUNTIME.txt')
Copy-Item -LiteralPath (Join-Path $root 'python\windows\VC-RUNTIME-LICENSE.txt') -Destination (Join-Path $App 'notices\Microsoft-VC-RUNTIME-LICENSE.txt')

# Validate actual imports and origins before marking this folder as managed.
# Poisoned Python environment is ignored.
$manifest = & (Join-Path $decisionDir 'python.exe') -I (Join-Path $root 'python\windows\verify_runtime.py') $App
if ($LASTEXITCODE -ne 0) { throw 'bundled runtime verification failed' }
[IO.File]::WriteAllText((Join-Path $App 'runtime\manifest.json.tmp'), ($manifest -join [Environment]::NewLine), (New-Object System.Text.UTF8Encoding($false)))
# Delete only the builder staging directory created by this invocation.
Remove-Item -Recurse -Force $stage
# bundle-windows relocates and re-verifies before publishing this marker.
