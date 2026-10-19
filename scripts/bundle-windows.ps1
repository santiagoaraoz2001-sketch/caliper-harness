# Native Windows amd64 developer folder. No downloads or private runtime claims.
param([Parameter(Mandatory=$true)][string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT' -or $env:PROCESSOR_ARCHITECTURE -ne 'AMD64') {
    throw 'Native Windows amd64 builder required.'
}
foreach ($tool in @('go', 'gcc', 'rg', 'bash')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Installed builder prerequisite missing: $tool" }
}
$root = Split-Path -Parent $PSScriptRoot
$out = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $out) { throw 'Output must not exist; no existing folder is reused.' }
# New-Item without Force is the exclusive output reservation; failures retain evidence.
New-Item -ItemType Directory -Path $out | Out-Null
Set-Content -LiteralPath (Join-Path $out 'BUILD-INCOMPLETE') -Value 'Build incomplete; do not launch.'
Push-Location $root
try {
    foreach ($dir in @('bin', 'notices', 'templates', 'python', 'python/decision')) {
        New-Item -ItemType Directory -Path (Join-Path $out $dir) | Out-Null
    }
    if (-not $env:GOPATH) { $env:GOPATH = Join-Path $env:TEMP 'go-path' }
    if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $env:TEMP 'go-build-cache' }
    $env:GOTOOLCHAIN = 'local'; $env:GOPROXY = 'off'; $env:GOSUMDB = 'off'
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '1'; $env:CC = 'gcc'
    $module = & go list -mod=readonly -m -f '{{.Version}}|{{.Dir}}' github.com/kuzudb/go-kuzu
    if ($LASTEXITCODE -ne 0 -or -not $module.StartsWith('v0.11.3|')) { throw 'Cached Kuzu v0.11.3 required.' }
    $kuzu = $module.Substring('v0.11.3|'.Length)
    & go build -mod=readonly -o (Join-Path $out 'caliper-harness.exe') ./cmd/specific-harness
    if ($LASTEXITCODE -ne 0) { throw 'Native Go/CGO build failed.' }
    Copy-Item -LiteralPath (Join-Path $kuzu 'lib/dynamic/windows/kuzu_shared.dll') -Destination $out
    Copy-Item -LiteralPath (Join-Path $kuzu 'LICENSE') -Destination (Join-Path $out 'notices/Kuzu-LICENSE')
    Copy-Item -LiteralPath (Get-Command rg).Source -Destination (Join-Path $out 'bin/rg.exe')
    Copy-Item -LiteralPath 'internal/prompts/prompts.yaml' -Destination (Join-Path $out 'prompts.yaml')
    Copy-Item -Path 'templates/*.tmpl' -Destination (Join-Path $out 'templates')
    Copy-Item -LiteralPath 'internal/prompts/prompts.yaml' -Destination (Join-Path $out 'templates/prompts.yaml')
    foreach ($file in @('server.py', 'backends.py', 'mps_attention.py', 'prompts.py', 'requirements.txt', 'requirements-system-one.txt')) {
        Copy-Item -LiteralPath (Join-Path 'python/decision' $file) -Destination (Join-Path $out 'python/decision')
    }
    Copy-Item -LiteralPath 'LICENSE', 'FIRST-USE.md', 'FIRST-USE-WINDOWS.md', 'SERVER-MODELS.md' -Destination $out
    Copy-Item -LiteralPath 'scripts/install-windows.ps1' -Destination $out
    # Omit the explicit source-relative template override so config resolves the
    # bundled templates beside the executable when launched from another project.
    $example = [IO.File]::ReadAllText((Join-Path $root 'caliper-harness.yaml.example'))
    $example = [regex]::Replace($example, '(?m)^templates:\r?\n(?:^[ \t]+.*(?:\r?\n|$))*', '')
    [IO.File]::WriteAllText((Join-Path $out 'caliper-harness.yaml.example'), $example, (New-Object System.Text.UTF8Encoding($false)))
    Copy-Item -LiteralPath 'scripts/start-windows.ps1' -Destination (Join-Path $out 'start.ps1')
    & go version -m (Join-Path $out 'caliper-harness.exe') | Set-Content (Join-Path $out 'notices/BUILD.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Unable to record executable build metadata.' }
    Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $out 'caliper-harness.exe'), (Join-Path $out 'kuzu_shared.dll'), (Join-Path $out 'bin/rg.exe'), (Join-Path $out 'prompts.yaml') | Format-List | Out-File (Join-Path $out 'notices/ARTIFACT-SHA256.txt')
    Add-Content (Join-Path $out 'notices/BUILD.txt') 'Developer folder only. Requires compatible native DLL dependencies, Bash, installed server runtimes, project tools and system Python for the optional decision sidecar. Native dependency audit and runtime qualification remain required.'
    # No runtime/manifest.json: there is no private managed Python environment.
    Remove-Item -LiteralPath (Join-Path $out 'BUILD-INCOMPLETE')
    Write-Output "Developer folder built; native qualification pending: $out"
} finally { Pop-Location }
