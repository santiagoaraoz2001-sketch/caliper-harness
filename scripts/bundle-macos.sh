#!/bin/bash
# Internal Mac arm64 development folder with private runtime dependencies.
# Builder acquires pinned packages; end users do not run Python setup commands.
set -euo pipefail
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || { echo 'Mac arm64 builder required' >&2; exit 1; }
[[ $# == 1 && -n $1 ]] || { echo 'usage: bundle-macos.sh OUTPUT_DIRECTORY' >&2; exit 2; }
case $1 in /*) out=$1 ;; *) out=$PWD/$1 ;; esac
[[ ! -L $out && ( ! -e $out || -d $out ) ]] || { echo 'output must be a directory, not a symlink' >&2; exit 1; }
[[ ! -d $out || -z $(ls -A "$out") ]] || { echo 'refusing nonempty output directory' >&2; exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
for tool in go uv curl tar otool install_name_tool lipo codesign shasum; do
  command -v "$tool" >/dev/null || { echo "builder tool missing: $tool" >&2; exit 1; }
done
export GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local GOPROXY=off
module=$(GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local go list -mod=readonly -m -f '{{.Version}} {{.Dir}}' github.com/kuzudb/go-kuzu)
[[ $module == v0.11.3\ * ]] || { echo 'expected pinned Kuzu v0.11.3' >&2; exit 1; }
kuzu_dir=${module#* }
mkdir -p "$out/lib" "$out/python/worker" "$out/python/decision" "$out/python/macos" "$out/notices"
MACOSX_DEPLOYMENT_TARGET=14.0 CGO_CFLAGS='-O2 -g -mmacosx-version-min=14.0' CGO_CXXFLAGS='-O2 -g -mmacosx-version-min=14.0' CGO_LDFLAGS='-O2 -g -mmacosx-version-min=14.0' GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -mod=readonly -ldflags '-X specific-harness/internal/bundle.Required=1' -o "$out/caliper-harness" ./cmd/specific-harness
lipo "$kuzu_dir/lib/dynamic/darwin/libkuzu.dylib" -thin arm64 -output "$out/lib/libkuzu.dylib"
cp internal/prompts/prompts.yaml "$out/prompts.yaml"
cp python/worker/server.py python/worker/framing.py python/worker/continuation.py "$out/python/worker/"
for name in server.py backends.py mps_attention.py prompts.py requirements.txt requirements-system-one.txt; do cp "python/decision/$name" "$out/python/decision/"; done
cp python/macos/verify_runtime.py "$out/python/macos/"
./scripts/prepare-macos-runtime.sh "$out"
# The source example deliberately pins a project-relative path. Bundle defaults
# must remain omitted so a copied example can use executable-relative assets.
awk '/^templates:$/ {skip=1; next} skip && /^[[:space:]]/ {next} skip {skip=0} {print}' caliper-harness.yaml.example > "$out/caliper-harness.yaml.example"
cp internal/tui/thirdparty/textarea/LICENSE "$out/notices/textarea-LICENSE"
cp LICENSE FIRST-USE.md PROMPTS.md "$out/"
cp scripts/install.sh "$out/install.sh"
chmod +x "$out/install.sh"
goroot=$(GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local go env GOROOT)
go_license=$goroot/LICENSE
[[ -f $go_license ]] || go_license=$goroot/../LICENSE # Homebrew keeps it beside libexec.
cp "$go_license" "$out/notices/Go-LICENSE"
GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local go version -m "$out/caliper-harness" > "$out/notices/DEPENDENCIES.txt"
while read -r name version; do
  dir=$(GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local go list -mod=readonly -m -f '{{.Dir}}' "$name")
  [[ -n $dir ]] || continue
  label=${name//\//_}
  for file in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE*; do
    [[ -f $file ]] && cp "$file" "$out/notices/${label}-$(basename "$file")"
  done
done < <(awk '$1 == "dep" {print $2, $3}' "$out/notices/DEPENDENCIES.txt")
{
  echo 'Internal development bundle: local ad-hoc signing, not a public installer.'
  echo 'Private standalone Python, hash-locked runtime packages and ripgrep supplied.'
  echo 'Project check tools, model weights and Ollama are not supplied.'
  echo 'Kuzu v0.11.3 input:'
  shasum -a 256 "$kuzu_dir/lib/dynamic/darwin/libkuzu.dylib"
  echo 'Go builder:'
  GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache GOTOOLCHAIN=local go version
} > "$out/notices/BUILD.txt"
install_name_tool -rpath "$kuzu_dir/lib/dynamic/darwin" '@executable_path/lib' "$out/caliper-harness"
for file in "$out/lib/libkuzu.dylib" "$out/caliper-harness"; do
  [[ $(lipo -archs "$file") == arm64 ]] || { echo 'unexpected bundle architecture' >&2; exit 1; }
  otool -L "$file" | tail -n +2 | while read -r dep rest; do
    case $dep in /usr/lib/*|/System/Library/*|@rpath/libkuzu.dylib) ;; *) echo "unbundled dependency: $dep" >&2; exit 1 ;; esac
  done
  codesign --force --sign - "$file"
  codesign --verify --strict "$file"
done
"$out/runtime/worker/bin/python3" -I scripts/audit-macos-native.py "$out" > "$out/notices/NATIVE-AUDIT.txt"
# A successful build must import after relocation, outside the source tree.
relocation=$(mktemp -d /private/tmp/caliper-runtime-relocation.XXXXXX)
mv "$out" "$relocation/Caliper With Spaces"
(
  cd "$relocation"
  "$relocation/Caliper With Spaces/runtime/worker/bin/python3" -I "$relocation/Caliper With Spaces/python/macos/verify_runtime.py" "$relocation/Caliper With Spaces" > "$relocation/Caliper With Spaces/notices/RELOCATION.json"
)
mv "$relocation/Caliper With Spaces" "$out"
rmdir "$relocation"
mv "$out/runtime/manifest.json.tmp" "$out/runtime/manifest.json"
rpaths=$(otool -l "$out/caliper-harness" | awk '/cmd LC_RPATH/ {r=1; next} r && /path / {print $2; r=0}')
[[ $rpaths == '@executable_path/lib' ]] || { echo "unexpected RPATH: $rpaths" >&2; exit 1; }
"$out/runtime/worker/bin/python3" -I python/macos/write_manifest.py "$out"
printf 'Development bundle: %s\n' "$out"
