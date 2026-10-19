#!/bin/bash
# Builder-only dependency acquisition into a new private development bundle.
# Users receive the finished runtime; a healthy launch never runs uv/pip.
# Only a startup verification failure, with an explicit yes from the user,
# re-downloads these pinned dependencies (internal/bundle Repair).
set -euo pipefail
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || { echo 'Mac arm64 builder required' >&2; exit 1; }
[[ $# == 1 ]] || { echo 'usage: prepare-macos-runtime.sh APPLICATION_DIRECTORY' >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
app=$(cd "$1" && pwd)
[[ ! -e "$app/runtime" ]] || { echo 'refusing existing runtime; use a new bundle output' >&2; exit 1; }
for tool in uv curl tar shasum; do command -v "$tool" >/dev/null || { echo "builder tool missing: $tool" >&2; exit 1; }; done
stage="$app/.runtime-build"
mkdir "$stage"
export UV_CACHE_DIR="${CALIPER_BUILD_CACHE:-$stage/cache}"
python_archive=cpython-3.12.12+20260203-aarch64-apple-darwin-install_only_stripped.tar.gz
python_url="https://github.com/astral-sh/python-build-standalone/releases/download/20260203/cpython-3.12.12%2B20260203-aarch64-apple-darwin-install_only_stripped.tar.gz"
curl -fsSL --retry 2 "$python_url" -o "$stage/$python_archive"
printf '%s  %s\n' 'f1a4a44f7e5c4a9a4a87f4dde275b500a4dbee70e1681714da6766832876eac5' "$stage/$python_archive" | shasum -a 256 -c -
tar -xzf "$stage/$python_archive" -C "$stage"
base="$stage/python"
[[ -x "$base/bin/python3" ]] || { echo 'standalone Python missing after download' >&2; exit 1; }
mkdir -p "$app/runtime" "$app/bin" "$app/notices"
cp "$root/python/macos/sources.json" "$app/runtime/sources.json"
for profile in worker decision; do
  mkdir "$app/runtime/$profile"
  cp -R "$base/." "$app/runtime/$profile/"
  # uv marks its standalone base externally managed. This override applies
  # only to the fresh copied interpreter under this owned output folder.
  MACOSX_DEPLOYMENT_TARGET=14.0 uv --no-config pip sync --python "$app/runtime/$profile/bin/python3" --python-platform aarch64-apple-darwin --system --break-system-packages --require-hashes --only-binary :all: --link-mode copy "$root/python/macos/$profile.lock"
  cp "$root/python/macos/$profile.lock" "$app/runtime/$profile/requirements.lock"
done
rg_archive=ripgrep-15.1.0-aarch64-apple-darwin.tar.gz
curl -fsSL --retry 2 "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/$rg_archive" -o "$stage/$rg_archive"
printf '%s  %s\n' '378e973289176ca0c6054054ee7f631a065874a352bf43f0fa60ef079b6ba715' "$stage/$rg_archive" | shasum -a 256 -c -
tar -xzf "$stage/$rg_archive" -C "$stage"
cp "$stage/ripgrep-15.1.0-aarch64-apple-darwin/rg" "$app/bin/rg"
for license in LICENSE-MIT LICENSE-APACHE UNLICENSE; do
  [[ ! -f "$stage/ripgrep-15.1.0-aarch64-apple-darwin/$license" ]] || cp "$stage/ripgrep-15.1.0-aarch64-apple-darwin/$license" "$app/notices/ripgrep-$license"
done
# Validate actual imports, origins and existing worker hook contracts before
# marking this folder as managed. Poisoned Python environment is ignored.
"$app/runtime/worker/bin/python3" -I "$root/python/macos/verify_runtime.py" "$app" > "$app/runtime/manifest.json.tmp"
# Delete only the builder staging directory created by this invocation.
rm -rf "$stage"
# bundle-macos signs/audits and relocation-tests before publishing this marker.
