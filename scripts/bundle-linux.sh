#!/usr/bin/env bash
# Native Linux development bundle. No runtime or model downloads.
set -euo pipefail
[[ $(uname -s) == Linux ]] || { echo 'Native Linux builder required; no cross-build qualification.' >&2; exit 1; }
[[ $# == 1 && -n $1 ]] || { echo 'usage: bundle-linux.sh NEW_OUTPUT_DIRECTORY' >&2; exit 2; }
case $(uname -m) in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo 'Supported builders: Linux amd64 or arm64' >&2; exit 1 ;; esac
for tool in go cc rg ldd sha256sum; do command -v "$tool" >/dev/null || { echo "Required installed builder tool: $tool" >&2; exit 1; }; done
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
case $1 in /*) out=$1 ;; *) out=$PWD/$1 ;; esac
# mkdir without -p is an exclusive reservation. Existing paths, including empty
# directories/symlinks, are never reused or deleted. Failures preserve evidence.
mkdir -- "$out" || { echo 'Output must be a new directory under an existing parent.' >&2; exit 1; }
printf 'Build incomplete; do not launch.\n' > "$out/BUILD-INCOMPLETE"
trap 'echo "Build failed; preserved incomplete output: $out" >&2' ERR
mkdir -- "$out/lib" "$out/bin" "$out/notices" "$out/python" "$out/templates"
cd "$root"
export GOPATH=${GOPATH:-/tmp/go-path} GOCACHE=${GOCACHE:-/tmp/go-build-cache} GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
module=$(GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local go list -mod=readonly -m -f '{{.Version}} {{.Dir}}' github.com/kuzudb/go-kuzu)
[[ $module == v0.11.3\ * ]] || { echo 'Cached Kuzu v0.11.3 required.' >&2; exit 1; }
kuzu_dir=${module#* }
cp -- "$kuzu_dir/lib/dynamic/linux-$arch/libkuzu.so" "$out/lib/"
GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local GOOS=linux GOARCH=$arch CGO_ENABLED=1 go build -mod=readonly -o "$out/caliper-harness" ./cmd/specific-harness
cp -- "$(command -v rg)" "$out/bin/rg"
cp -- internal/prompts/prompts.yaml "$out/prompts.yaml"
cp -- templates/*.tmpl "$out/templates/"
cp -- internal/prompts/prompts.yaml "$out/templates/prompts.yaml"
mkdir -- "$out/python/decision"
cp -- python/decision/server.py python/decision/backends.py python/decision/mps_attention.py python/decision/prompts.py python/decision/requirements*.txt "$out/python/decision/"
cp -- "$kuzu_dir/LICENSE" "$out/notices/Kuzu-LICENSE"
cp -- LICENSE FIRST-USE.md "$out/"
awk '/^templates:$/ {skip=1; next} skip && /^[[:space:]]/ {next} skip {skip=0} {print}' caliper-harness.yaml.example > "$out/caliper-harness.yaml.example"
cp -- scripts/start-linux.sh "$out/start.sh"
chmod +x "$out/start.sh"
{
 printf 'Linux %s development bundle. Native build; not release-qualified.\n' "$arch"
 printf 'Requires compatible system C/C++ libraries shown below, bash, selected inference servers and project tools.\n'
 printf 'Decision sidecar requires system Python and its requirements; no private Python, model weights or server runtimes are bundled.\n'
 printf 'MLX worker is not provided on Linux. Configure an installed compatible server.\n'
 GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local go version
 GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local go version -m "$out/caliper-harness"
} > "$out/notices/BUILD.txt"
LD_LIBRARY_PATH="$out/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" ldd "$out/caliper-harness" > "$out/notices/NATIVE-DEPENDENCIES.txt"
ldd "$out/lib/libkuzu.so" >> "$out/notices/NATIVE-DEPENDENCIES.txt"
if rg_deps=$(ldd "$out/bin/rg" 2>&1); then
  printf '%s\n' "$rg_deps" >> "$out/notices/NATIVE-DEPENDENCIES.txt"
elif [[ $rg_deps == *'not a dynamic executable'* || $rg_deps == *'statically linked'* ]]; then
  printf 'ripgrep: %s\n' "$rg_deps" >> "$out/notices/NATIVE-DEPENDENCIES.txt"
else
  printf '%s\n' "$rg_deps" >&2
  exit 1
fi
if grep -q 'not found' "$out/notices/NATIVE-DEPENDENCIES.txt"; then echo 'Missing native dependency; see notices.' >&2; exit 1; fi
(cd "$out" && sha256sum caliper-harness lib/libkuzu.so bin/rg prompts.yaml start.sh > notices/ARTIFACT-SHA256.txt)
# No managed runtime manifest: bundle.Check must never mistake this source
# distribution for the self-contained macOS runtime.
rm -- "$out/BUILD-INCOMPLETE"
printf 'Development folder built, native behavior still requires qualification: %s\n' "$out"
