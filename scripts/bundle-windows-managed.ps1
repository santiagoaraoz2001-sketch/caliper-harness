# Internal Windows x64 development folder with private runtime dependencies.
# Builder acquires pinned packages; end users do not run Python setup commands.
# The bundle's command is `caliper` (caliper.exe); install-windows.ps1 puts
# the folder on the user's PATH.
#Requires -Version 5.1
param([Parameter(Mandatory = $true)][string]$Out, [switch]$AllowDownloads)
$ErrorActionPreference = 'Stop'
if (-not $AllowDownloads) { throw 'Private runtime acquisition requires -AllowDownloads; use bundle-windows.ps1 for offline developer packaging.' }
if ($env:OS -ne 'Windows_NT' -or $env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'Windows x64 builder required' }
if (-not $Out) { throw 'usage: bundle-windows.ps1 OUTPUT_DIRECTORY' }
$Out = [System.IO.Path]::GetFullPath($Out)
if (Test-Path -LiteralPath $Out) { throw 'Output must not exist.' }
New-Item -ItemType Directory -Path $Out | Out-Null
Set-Content -LiteralPath (Join-Path $Out 'BUILD-INCOMPLETE') -Value 'Incomplete build; do not launch.'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Push-Location $root
try {
# Kuzu needs a C compiler for its cgo glue; everything else is pure Go.
foreach ($tool in 'go', 'uv', 'curl.exe', 'tar.exe', 'gcc', 'bash') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        if ($tool -eq 'gcc') { throw 'builder tool missing: gcc (mingw-w64, e.g. via MSYS2) - required for the Kuzu cgo build' }
        throw "builder tool missing: $tool"
    }
}
$env:GOPATH = Join-Path $env:TEMP 'caliper-go-path'
$env:GOCACHE = Join-Path $env:TEMP 'caliper-go-build-cache'
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'; $env:GOSUMDB = 'off'; $env:CC = 'gcc'
$module = & go list -mod=readonly -m -f '{{.Version}} {{.Dir}}' github.com/kuzudb/go-kuzu
if ($LASTEXITCODE -ne 0 -or $module -notmatch '^v0\.11\.3 ') { throw 'expected pinned Kuzu v0.11.3 (run `go mod download` once)' }
$kuzuDir = ($module -split ' ', 2)[1].Trim()

New-Item -ItemType Directory -Force -Path (Join-Path $Out 'python\decision'), (Join-Path $Out 'python\windows'), (Join-Path $Out 'notices'), (Join-Path $Out 'bin'), (Join-Path $Out 'templates') | Out-Null
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '1'
& go build -mod=readonly -ldflags '-X specific-harness/internal/bundle.Required=1' -o (Join-Path $Out 'caliper.exe') ./cmd/specific-harness
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
# Windows finds DLLs next to the executable; there is no rpath to rewrite.
$dyn = Join-Path $kuzuDir 'lib\dynamic\windows'
if (Test-Path $dyn) {
    Get-ChildItem (Join-Path $dyn '*.dll') | Copy-Item -Destination $Out
    if (-not (Get-ChildItem (Join-Path $Out '*.dll'))) { throw "no Kuzu DLL found in $dyn" }
} else {
    throw "unexpected Kuzu library layout under $kuzuDir\lib"
}

Copy-Item 'templates\*.tmpl' (Join-Path $Out 'templates')
Copy-Item 'internal\prompts\prompts.yaml' (Join-Path $Out 'templates\prompts.yaml')
Copy-Item 'LICENSE' $Out
Copy-Item 'internal\prompts\prompts.yaml' (Join-Path $Out 'prompts.yaml')
foreach ($name in 'server.py', 'backends.py', 'mps_attention.py', 'prompts.py', 'requirements.txt', 'requirements-system-one.txt') {
    Copy-Item (Join-Path $root "python\decision\$name") (Join-Path $Out 'python\decision')
}
Copy-Item (Join-Path $root 'python\windows\verify_runtime.py') (Join-Path $Out 'python\windows')
& (Join-Path $root 'scripts\prepare-windows-runtime.ps1') -App $Out -AllowDownloads
# The source example deliberately pins a project-relative path. Bundle defaults
# must remain omitted so a copied example can use executable-relative assets.
$skip = $false
$stripped = foreach ($line in (Get-Content (Join-Path $root 'caliper-harness.yaml.example'))) {
    if ($line -match '^templates:') { $skip = $true; continue }
    if ($skip -and $line -match '^\s') { continue }
    $skip = $false
    $line
}
[IO.File]::WriteAllText((Join-Path $Out 'caliper-harness.yaml.example'), ($stripped -join [Environment]::NewLine), (New-Object System.Text.UTF8Encoding($false)))
Copy-Item (Join-Path $root 'internal\tui\thirdparty\textarea\LICENSE') (Join-Path $Out 'notices\textarea-LICENSE')
Copy-Item (Join-Path $root 'FIRST-USE-WINDOWS.md') $Out
Copy-Item (Join-Path $root 'SERVER-MODELS.md') $Out
Copy-Item (Join-Path $root 'scripts\install-windows.ps1') $Out
$goroot = (& go env GOROOT).Trim()
Copy-Item (Join-Path $goroot 'LICENSE') (Join-Path $Out 'notices\Go-LICENSE')
& go version -m (Join-Path $Out 'caliper.exe') | Out-File -Encoding utf8 (Join-Path $Out 'notices\DEPENDENCIES.txt')
foreach ($line in (Get-Content (Join-Path $Out 'notices\DEPENDENCIES.txt'))) {
    $parts = $line.Trim() -split '\s+'
    if ($parts[0] -ne 'dep' -or $parts.Count -lt 3) { continue }
    $dir = (& go list -mod=readonly -m -f '{{.Dir}}' $parts[1]).Trim()
    if (-not $dir) { continue }
    $label = $parts[1] -replace '/', '_'
    Get-ChildItem -LiteralPath $dir -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like 'LICENSE*' -or $_.Name -like 'LICENCE*' -or $_.Name -like 'COPYING*' -or $_.Name -like 'NOTICE*' } |
        ForEach-Object { Copy-Item $_.FullName (Join-Path $Out ("notices\$label-$($_.Name)")) }
}
$kuzuInput = Get-ChildItem (Join-Path $dyn '*.dll') -ErrorAction SilentlyContinue | Select-Object -First 1
$kuzuNote = if ($kuzuInput) { (Get-FileHash -Algorithm SHA256 $kuzuInput.FullName).Hash + '  ' + $kuzuInput.Name } else { 'static' }
@(
    'Internal development bundle: unsigned, not a public installer.'
    'Private standalone Python, hash-locked runtime packages and ripgrep supplied.'
    'Project check tools, model weights and Ollama are not supplied.'
    "Kuzu v0.11.3 input: $kuzuNote"
    ("Go builder: " + (& go version))
) | Out-File -Encoding utf8 (Join-Path $Out 'notices\BUILD.txt')

# A successful build must import after relocation, outside the source tree.
$relocation = Join-Path $env:TEMP ('caliper-runtime-relocation-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $relocation | Out-Null
Move-Item -LiteralPath $Out -Destination (Join-Path $relocation 'Caliper With Spaces')
$relocated = Join-Path $relocation 'Caliper With Spaces'
& (Join-Path $relocated 'runtime\decision\python.exe') -I (Join-Path $relocated 'python\windows\verify_runtime.py') $relocated |
    Out-File -Encoding utf8 (Join-Path $relocated 'notices\RELOCATION.json')
if ($LASTEXITCODE -ne 0) { throw 'relocated runtime verification failed' }
Move-Item -LiteralPath $relocated -Destination $Out
Remove-Item -LiteralPath $relocation
# Publish the marker only after relocation imports succeeded.
Move-Item -LiteralPath (Join-Path $Out 'runtime\manifest.json.tmp') -Destination (Join-Path $Out 'runtime\manifest.json')
Remove-Item -LiteralPath (Join-Path $Out 'BUILD-INCOMPLETE')
# Inventory must describe the final artifact, excluding the removed failure marker.
& (Join-Path $Out 'runtime\decision\python.exe') -I (Join-Path $root 'python\windows\write_manifest.py') $Out
if ($LASTEXITCODE -ne 0) {
    Set-Content -LiteralPath (Join-Path $Out 'BUILD-INCOMPLETE') -Value 'Inventory failed; do not launch.'
    throw 'could not finalize bundle inventory'
}
Write-Output "Development bundle built; native application qualification remains required: $Out"
} finally { Pop-Location }
