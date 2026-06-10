#!/usr/bin/env bash
# install.sh — build and install gpty + gmux on macOS / Linux.
#
# Builds two static binaries (no runtime deps beyond tmux), installs them to a
# bin dir on PATH, installs the tmux.conf, and prints the MCP registration.
# Missing prerequisites (Go, tmux) are offered for install via the system
# package manager: brew (macOS), apt/dnf/pacman/zypper/apk (Linux).
# Idempotent: re-running is safe.
#
# Flags / env:
#   --yes | GPTY_YES=1              answer yes to all install offers (non-interactive)
#   --tmux-alias | GPTY_TMUX_ALIAS=1  also link gmux as `tmux` in the bin dir
#                                   (gmux commands by tmux's name; everything
#                                   else still reaches the real tmux). Opt-in
#                                   on Unix because it can shadow the system
#                                   tmux depending on PATH order.
#   GPTY_BIN=<dir>       install destination (default /usr/local/bin or ~/.local/bin)
set -euo pipefail

info() { printf '==> %s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }

ASSUME_YES="${GPTY_YES:-}"
TMUX_ALIAS="${GPTY_TMUX_ALIAS:-}"
# (case, not `[[ ]] &&`: under set -e a non-matching && list would abort)
for a in "$@"; do
    case "$a" in
        --yes|-y)     ASSUME_YES=1 ;;
        --tmux-alias) TMUX_ALIAS=1 ;;
    esac
done

# confirm <question> — yes/no with default yes. Reads /dev/tty so it still
# prompts under `curl | bash`; with no usable tty (CI), it assumes yes, same as
# the Windows installer. (2>/dev/null comes first so a failed /dev/tty open is
# silent and routes to the no-tty branch.)
confirm() {
    [[ -n "$ASSUME_YES" ]] && return 0
    local ans
    if read -r -p "$1 [Y/n] " ans 2>/dev/null < /dev/tty; then
        [[ -z "$ans" || "$ans" =~ ^[Yy] ]]
    else
        info "(no tty — assuming yes: $1)"
        return 0
    fi
}

# detect_pm — print the available package manager, or nothing.
detect_pm() {
    if [[ "$(uname)" == "Darwin" ]]; then
        command -v brew >/dev/null 2>&1 && echo brew
        return 0
    fi
    local pm
    for pm in apt-get dnf pacman zypper apk; do
        if command -v "$pm" >/dev/null 2>&1; then echo "$pm"; return 0; fi
    done
}

SUDO=""
if [[ "$(id -u)" -ne 0 ]] && command -v sudo >/dev/null 2>&1; then SUDO="sudo"; fi

# pm_install <pm> <pkg>
pm_install() {
    case "$1" in
        brew)    brew install "$2" ;;
        apt-get) $SUDO apt-get update -qq && $SUDO apt-get install -y "$2" ;;
        dnf)     $SUDO dnf install -y "$2" ;;
        pacman)  $SUDO pacman -S --noconfirm --needed "$2" ;;
        zypper)  $SUDO zypper --non-interactive install "$2" ;;
        apk)     $SUDO apk add "$2" ;;
        *)       return 1 ;;
    esac
}

# go_pkg <pm> — the Go package name on each package manager.
go_pkg() {
    case "$1" in
        apt-get) echo golang-go ;;
        dnf)     echo golang ;;
        *)       echo go ;;  # brew, pacman, zypper, apk
    esac
}

# offer_install <human-name> <pkg-resolver: fixed name or "GO"> — offer to
# install via the detected package manager; silent no-op if declined/absent.
offer_install() {
    local name="$1" pkg="$2" pm
    pm="$(detect_pm)"
    if [[ -z "$pm" ]]; then
        return 1
    fi
    [[ "$pkg" == "GO" ]] && pkg="$(go_pkg "$pm")"
    if confirm "$name not found — install '$pkg' via $pm?"; then
        info "Installing $pkg via $pm..."
        pm_install "$pm" "$pkg"
    fi
}

# --- Go (build-time requirement) ---------------------------------------------
if ! command -v go >/dev/null 2>&1; then
    offer_install "Go" "GO" || true
    command -v go >/dev/null 2>&1 || err "Go is required to build gpty. Install it (https://go.dev/dl) and re-run."
fi

# go.mod targets a recent toolchain; any Go >= 1.21 auto-fetches the right one
# (GOTOOLCHAIN). Older distro packages can't, so fail with a clear message.
gover="$(go env GOVERSION 2>/dev/null || true)"
if [[ "$gover" =~ ^go([0-9]+)\.([0-9]+) ]]; then
    if (( BASH_REMATCH[1] == 1 && BASH_REMATCH[2] < 21 )); then
        err "Go $gover is too old (< 1.21 cannot auto-fetch the toolchain go.mod needs). Upgrade Go and re-run."
    fi
fi

# --- tmux (runtime requirement) ----------------------------------------------
if ! command -v tmux >/dev/null 2>&1; then
    offer_install "tmux" "tmux" || true
    command -v tmux >/dev/null 2>&1 || err "tmux is required at runtime. Install it (e.g. 'brew install tmux' / 'sudo apt-get install tmux') and re-run."
fi

VERSION="$(git -C "$(dirname "$0")" describe --tags --always 2>/dev/null || echo dev)"
LDFLAGS="-s -w -X github.com/samdotson61/gpty/internal/buildinfo.Version=${VERSION}"

# Pick a bin dir on PATH we can write to.
BIN="${GPTY_BIN:-}"
if [[ -z "$BIN" ]]; then
    if [[ -w /usr/local/bin ]]; then BIN=/usr/local/bin; else BIN="$HOME/.local/bin"; fi
fi
mkdir -p "$BIN"

cd "$(dirname "$0")"
info "Building gpty + gmux (${VERSION})..."
go build -ldflags "$LDFLAGS" -o "$BIN/gpty" ./cmd/gpty
go build -ldflags "$LDFLAGS" -o "$BIN/gmux" ./cmd/gmux
info "Installed: $BIN/gpty, $BIN/gmux"

# Optional `tmux` alias: gmux dispatches on its invoked name, and gpty/gmux
# skip this alias when resolving the real tmux, so it cannot recurse.
if [[ -n "$TMUX_ALIAS" ]]; then
    ln -sf "$BIN/gmux" "$BIN/tmux"
    info "Installed tmux alias: $BIN/tmux -> gmux (shadows the system tmux while $BIN precedes it on PATH)"
fi

info "Installing tmux.conf..."
"$BIN/gpty" setup >/dev/null || info "(gpty setup reported a warning; check 'gpty setup' output)"

case ":$PATH:" in
  *":$BIN:"*) : ;;
  *) info "NOTE: $BIN is not on your PATH — add it to your shell profile." ;;
esac

cat <<EOF

Done. Try:
  gpty doctor                          # verify tmux >= 3.2
  claude mcp add gpty -- gpty mcp      # register with Claude Code (local agents)
  gmux new -t test                     # open a session and attach
EOF
