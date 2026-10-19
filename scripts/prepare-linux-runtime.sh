#!/usr/bin/env bash
# Native builder only. End users receive the complete, verified runtime.
set -euo pipefail
[[ $(uname -s) == Linux && $# == 1 ]] || { echo 'usage: prepare-linux-runtime.sh APPLICATION_DIRECTORY (native Linux)' >&2; exit 2; }
case $(uname -m) in aarch64|arm64) arch=arm64; target=aarch64-unknown-linux-gnu ;; x86_64) arch=amd64; target=x86_64-unknown-linux-gnu ;; *) echo 'Unsupported architecture' >&2; exit 1 ;; esac
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
app=$(cd -- "$1" && pwd)
[[ ! -e $app/runtime ]] || { echo 'Use a new bundle output.' >&2; exit 1; }
for tool in uv curl tar sha256sum python3; do command -v "$tool" >/dev/null || { echo "Missing builder tool: $tool" >&2; exit 1; }; done
stage=$app/.runtime-build
mkdir "$stage" "$app/runtime"
mkdir -p "$app/bin" "$app/notices"
cp "$root/python/linux/sources-$arch.json" "$app/runtime/sources.json"
read_source() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]][sys.argv[3]])' "$app/runtime/sources.json" "$1" "$2"; }
for item in python ripgrep; do
  curl -fsSL --retry 2 "$(read_source "$item" url)" -o "$stage/$item.tar.gz"
  printf '%s  %s\n' "$(read_source "$item" sha256)" "$stage/$item.tar.gz" | sha256sum -c -
  tar -xzf "$stage/$item.tar.gz" -C "$stage"
done
mv "$stage/python" "$app/runtime/decision"
UV_CACHE_DIR=${CALIPER_BUILD_CACHE:-$stage/cache} uv --no-config pip sync --python "$app/runtime/decision/bin/python3" --python-platform "$target" --system --break-system-packages --require-hashes --only-binary :all: --link-mode copy "$root/python/linux/decision-$arch.lock"
cp "$root/python/linux/decision-$arch.lock" "$app/runtime/decision/requirements.lock"
rgdir=$(find "$stage" -mindepth 1 -maxdepth 1 -type d -name 'ripgrep-*')
[[ -n $rgdir ]] || { echo 'ripgrep archive missing.' >&2; exit 1; }
cp "$rgdir/rg" "$app/bin/rg"
for license in LICENSE-MIT LICENSE-APACHE UNLICENSE; do [[ ! -f $rgdir/$license ]] || cp "$rgdir/$license" "$app/notices/ripgrep-$license"; done
"$app/runtime/decision/bin/python3" -I "$root/python/linux/verify_runtime.py" "$app" > "$app/runtime/manifest.json.tmp"
rm -rf -- "$stage"
