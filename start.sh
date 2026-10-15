#!/bin/sh
# Launch the validated TUI while retaining the calling project's working directory.
set -eu
harness_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ui_binary="$harness_dir/verification/2026-10-13-managed-mac/app/caliper-harness"
if [ ! -x "$ui_binary" ]; then
    printf '%s\n' "Caliper Harness has not been built: $ui_binary" >&2
    exit 1
fi
exec "$ui_binary" tui "$@"
