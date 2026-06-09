#!/usr/bin/env bash
# install.sh — build and install gpty + gmux on macOS / Linux.
#
# Builds two static binaries (no runtime deps beyond tmux), installs them to a
# bin dir on PATH, installs the tmux.conf, and prints the MCP registration.
# Idempotent: re-running is safe.
set -euo pipefail

info() { printf '==> %s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || err "Go not found. Install Go from https://go.dev/dl and re-run."

# tmux: required at runtime. Offer the right install hint per OS.
if ! command -v tmux >/dev/null 2>&1; then
  if [[ "$(uname)" == "Darwin" ]]; then
    info "tmux not found — installing via Homebrew..."
    command -v brew >/dev/null 2>&1 && brew install tmux || err "Homebrew not found. brew install tmux, then re-run."
  else
    err "tmux not found. Install it (e.g. 'sudo apt-get install tmux') and re-run."
  fi
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
