#!/bin/sh
# Register this intact application folder. No downloads or administrator rights.
set -eu
usage() { echo 'usage: install.sh [--bin-dir DIRECTORY] [--no-path]' >&2; exit 2; }
bin_dir=$HOME/.local/bin
update_path=true
while [ "$#" -gt 0 ]; do
    case $1 in
        --bin-dir) [ "$#" -ge 2 ] || usage; bin_dir=$2; shift 2 ;;
        --no-path) update_path=false; shift ;;
        *) usage ;;
    esac
done
case $bin_dir in /*) ;; *) echo '--bin-dir must be an absolute path.' >&2; exit 2 ;; esac
app_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
[ ! -e "$app_dir/BUILD-INCOMPLETE" ] || { echo 'Cannot install an incomplete bundle.' >&2; exit 1; }
[ -x "$app_dir/caliper-harness" ] && [ -f "$app_dir/prompts.yaml" ] || {
    echo 'Run install.sh from the intact application folder, with caliper-harness and prompts.yaml beside it.' >&2; exit 1;
}
case $(uname -s) in
    Darwin) [ -f "$app_dir/lib/libkuzu.dylib" ] && [ -f "$app_dir/runtime/manifest.json" ] || {
        echo 'Incomplete Mac bundle: lib/libkuzu.dylib and runtime/manifest.json are required.' >&2; exit 1;
    } ;;
    Linux) [ -f "$app_dir/lib/libkuzu.so" ] || { echo 'Incomplete Linux bundle: lib/libkuzu.so is missing.' >&2; exit 1; } ;;
    *) echo 'This installer supports macOS and Linux. On Windows use install-windows.ps1.' >&2; exit 1 ;;
esac
# Quote literal paths for POSIX shells, including spaces and apostrophes.
quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
if "$update_path"; then
    selected_shell=${SHELL:-/bin/sh}
    shell_name=${selected_shell##*/}
    case $shell_name in
        zsh|bash|sh|dash|ksh) ;;
        *) echo "Shell ${SHELL:-unknown} needs manual PATH setup. Run with --no-path, then add $bin_dir to PATH." >&2; exit 1 ;;
    esac
fi
mkdir -p -- "$bin_dir"
launcher=$bin_dir/caliper
if [ -e "$launcher" ] || [ -L "$launcher" ]; then
    [ -f "$launcher" ] && [ ! -L "$launcher" ] && grep -q '^# Caliper installed launcher$' "$launcher" || {
        echo "Refusing to replace an unrelated command: $launcher. Choose another --bin-dir or move it yourself." >&2; exit 1;
    }
fi
tmp=$(mktemp "$bin_dir/.caliper-install.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
{
    printf '#!/bin/sh\n# Caliper installed launcher\nset -eu\napp_dir='
    quote "$app_dir"
    printf '\n'
    cat <<'LAUNCHER'
[ ! -e "$app_dir/BUILD-INCOMPLETE" ] || { echo 'Caliper application folder is an incomplete build.' >&2; exit 1; }
[ -x "$app_dir/caliper-harness" ] || { echo "Caliper application folder moved or is missing: $app_dir. Run its install.sh again." >&2; exit 1; }
PATH="$app_dir/bin:$PATH"
export PATH
if [ -f "$app_dir/lib/libkuzu.so" ]; then
    LD_LIBRARY_PATH="$app_dir/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    export LD_LIBRARY_PATH
fi
exec "$app_dir/caliper-harness" "$@"
LAUNCHER
} > "$tmp"
chmod 755 "$tmp"
mv -f -- "$tmp" "$launcher"
if "$update_path"; then
    path_line="export PATH=$(quote "$bin_dir"):\"\$PATH\""
    add_path() {
        if ! grep -Fqx -- "$path_line" "$1" 2>/dev/null; then
            printf '\n# Caliper terminal command\n%s\n' "$path_line" >> "$1"
        fi
    }
    case $shell_name in
        zsh) add_path "${ZDOTDIR:-$HOME}/.zshrc"; add_path "${ZDOTDIR:-$HOME}/.zprofile" ;;
        bash)
            add_path "$HOME/.bashrc"
            if [ -f "$HOME/.bash_profile" ]; then add_path "$HOME/.bash_profile"
            elif [ -f "$HOME/.bash_login" ]; then add_path "$HOME/.bash_login"
            else add_path "$HOME/.profile"; fi ;;
        *) add_path "$HOME/.profile" ;;
    esac
fi
printf 'Installed command: %s\nApplication folder: %s\n' "$launcher" "$app_dir"
printf 'Keep that application folder intact. To register a moved or updated folder, run its installer again.\n'
if "$update_path"; then
    printf 'Open a new terminal, enter your project directory, and run: caliper\n'
    printf 'For this terminal now, run: %s\n' "$path_line"
else
    printf 'Add %s to your shell PATH, then run: caliper\n' "$bin_dir"
fi
