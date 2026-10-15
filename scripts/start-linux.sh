#!/bin/sh
# Preserve the calling project's cwd and every argument verbatim.
set -eu
app_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -e "$app_dir/BUILD-INCOMPLETE" ]; then
    printf '%s\n' 'This application folder is an incomplete build; see notices.' >&2
    exit 1
fi
if [ ! -x "$app_dir/caliper-harness" ] || [ ! -f "$app_dir/lib/libkuzu.so" ]; then
    printf '%s\n' 'Missing executable or native library; keep the intact application folder.' >&2
    exit 1
fi
LD_LIBRARY_PATH="$app_dir/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
PATH="$app_dir/bin:$PATH"
export LD_LIBRARY_PATH PATH
exec "$app_dir/caliper-harness" tui "$@"
