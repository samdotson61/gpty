#!/bin/sh
# install-release.sh — install gpty + gmux from a published GitHub release.
# No Go toolchain needed (unlike install.sh, which builds from source).
#
#   curl -fsSL https://raw.githubusercontent.com/samdotson61/gpty/main/install-release.sh | sh
#
# Env:
#   GPTY_BIN=<dir>       install destination (default /usr/local/bin if writable, else ~/.local/bin)
#   GPTY_VERSION=vX.Y.Z  install a specific release (default: latest)
#
# POSIX sh on purpose: this runs on whatever the machine happens to have.
set -eu

REPO=samdotson61/gpty
info() { printf '==> %s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || err "$1 is required but not installed"; }
need uname
need tar

# curl or wget, whichever exists.
if command -v curl >/dev/null 2>&1; then
    fetch() { curl -fsSL "$1" -o "$2"; }
    fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO "$2" "$1"; }
    fetch_stdout() { wget -qO- "$1"; }
else
    err "need curl or wget"
fi

# --- platform ------------------------------------------------------------------
os=$(uname -s)
case "$os" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    *) err "unsupported OS '$os' — on Windows use install.ps1 (see the README)" ;;
esac

arch=$(uname -m)
case "$arch" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64)  arch=amd64 ;;
    *) err "unsupported architecture '$arch'" ;;
esac

# --- version -------------------------------------------------------------------
version="${GPTY_VERSION:-}"
if [ -z "$version" ]; then
    info "Resolving latest release..."
    version=$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
    [ -n "$version" ] || err "could not determine the latest release (rate-limited? set GPTY_VERSION=vX.Y.Z)"
fi
bare="${version#v}"

name="gpty_${bare}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$version"

# --- download + verify ---------------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

info "Downloading $name ($version)..."
fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || err "download failed — does release $version include $os/$arch?"

if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
    if command -v shasum >/dev/null 2>&1; then
        sha=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
    elif command -v sha256sum >/dev/null 2>&1; then
        sha=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
    else
        sha=""
        info "no shasum/sha256sum available — skipping checksum verification"
    fi
    if [ -n "$sha" ]; then
        want=$(grep "$name.tar.gz" "$tmp/checksums.txt" | awk '{print $1}' | head -n1)
        [ -n "$want" ] || err "checksums.txt has no entry for $name.tar.gz"
        [ "$sha" = "$want" ] || err "checksum mismatch for $name.tar.gz (got $sha, want $want)"
        info "Checksum verified."
    fi
else
    info "checksums.txt not published for $version — skipping verification"
fi

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"

# --- install -------------------------------------------------------------------
bin="${GPTY_BIN:-}"
if [ -z "$bin" ]; then
    if [ -w /usr/local/bin ]; then bin=/usr/local/bin; else bin="$HOME/.local/bin"; fi
fi
mkdir -p "$bin"

for exe in gpty gmux; do
    cp "$tmp/$name/$exe" "$bin/$exe"
    chmod +x "$bin/$exe"
done
info "Installed: $bin/gpty, $bin/gmux"

"$bin/gpty" setup >/dev/null 2>&1 || info "(gpty setup reported a warning; run 'gpty setup' to see it)"

case ":$PATH:" in
    *":$bin:"*) ;;
    *) info "NOTE: $bin is not on your PATH — add it to your shell profile." ;;
esac

# tmux is the one runtime dependency we don't ship.
if ! command -v tmux >/dev/null 2>&1; then
    info "tmux not found — gpty needs it at runtime. Install it (brew install tmux / apt install tmux), then run 'gpty doctor'."
fi

cat <<EOF

$("$bin/gpty" version)

Next:
  gpty doctor                          # verify tmux >= 3.2 + probe control mode
  claude mcp add gpty -- gpty mcp      # register with Claude Code (local agents)
  gmux new -t test                     # open a session and attach
EOF
