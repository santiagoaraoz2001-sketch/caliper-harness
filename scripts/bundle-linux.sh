#!/usr/bin/env bash
# Native Linux complete application bundle. Builder acquires pinned runtimes.
set -euo pipefail
[[ $(uname -s) == Linux ]] || { echo 'Native Linux builder required; no cross-build qualification.' >&2; exit 1; }
[[ $# == 1 && -n $1 ]] || { echo 'usage: bundle-linux.sh NEW_OUTPUT_DIRECTORY' >&2; exit 2; }
case $(uname -m) in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo 'Supported builders: Linux amd64 or arm64' >&2; exit 1 ;; esac
for tool in go cc uv curl python3 ldd sha256sum; do command -v "$tool" >/dev/null || { echo "Required installed builder tool: $tool" >&2; exit 1; }; done
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
GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local GOOS=linux GOARCH=$arch CGO_ENABLED=1 go build -mod=readonly -ldflags '-X specific-harness/internal/bundle.Required=1' -o "$out/caliper-harness" ./cmd/specific-harness

cp -- internal/prompts/prompts.yaml "$out/prompts.yaml"
cp -- templates/*.tmpl "$out/templates/"
cp -- internal/prompts/prompts.yaml "$out/templates/prompts.yaml"
mkdir -- "$out/python/decision" "$out/python/linux"
cp -- python/decision/server.py python/decision/backends.py python/decision/mps_attention.py python/decision/prompts.py python/decision/requirements*.txt "$out/python/decision/"
cp -- python/linux/verify_runtime.py "$out/python/linux/"
cp -- "$kuzu_dir/LICENSE" "$out/notices/Kuzu-LICENSE"
cp -- LICENSE FIRST-USE-LINUX.md SERVER-MODELS.md PROMPTS.md "$out/"
awk '/^templates:$/ {skip=1; next} skip && /^[[:space:]]/ {next} skip {skip=0} {print}' caliper-harness.yaml.example > "$out/caliper-harness.yaml.example"
cp -- scripts/start-linux.sh "$out/start.sh"
cp -- scripts/install.sh "$out/install.sh"
chmod +x "$out/install.sh"
chmod +x "$out/start.sh"
{
 printf 'Linux %s application bundle. Native behavior requires qualification.\n' "$arch"
 printf 'Requires compatible system C/C++ libraries shown below, bash, selected inference servers and project tools.\n'
 printf 'Private decision Python and pinned dependencies included. Model weights and external model servers remain user-managed.\n'
 printf 'MLX worker is not provided on Linux. Configure an installed compatible server.\n'
 GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local go version
 GOPATH="$GOPATH" GOCACHE="$GOCACHE" GOTOOLCHAIN=local go version -m "$out/caliper-harness"
} > "$out/notices/BUILD.txt"
bash "$root/scripts/prepare-linux-runtime.sh" "$out"
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
(cd "$out" && sha256sum caliper-harness lib/libkuzu.so bin/rg prompts.yaml start.sh install.sh > notices/ARTIFACT-SHA256.txt)
# Verify relocatability before selecting a complete runtime manifest.
relocation=$(mktemp -d /tmp/caliper-linux-relocation.XXXXXX)
mv -- "$out" "$relocation/Caliper With Spaces"
relocated="$relocation/Caliper With Spaces"
"$relocated/runtime/decision/bin/python3" -I "$relocated/python/linux/verify_runtime.py" "$relocated" > "$relocated/notices/RELOCATION.json"
mv -- "$relocated" "$out"
rmdir -- "$relocation"
mv -- "$out/runtime/manifest.json.tmp" "$out/runtime/manifest.json"
rm -- "$out/BUILD-INCOMPLETE"
"$out/runtime/decision/bin/python3" -I "$root/python/linux/write_manifest.py" "$out"
printf 'Complete Linux folder built; model inference remains separately qualified: %s\n' "$out"
