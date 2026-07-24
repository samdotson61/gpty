#!/usr/bin/env bash
# release.sh — one-command gpty release: cross-compile all six targets, package,
# checksum, tag, and publish a GitHub release with notes from the CHANGELOG.
#
#   scripts/release.sh v0.7.0
#
# Idempotent-ish: refuses a dirty tree, a version mismatch, or an existing
# release; re-running after a partial failure is safe up to the publish step.
set -euo pipefail

info() { printf '==> %s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }

TAG="${1:-}"
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || err "usage: scripts/release.sh vX.Y.Z"
VERSION="${TAG#v}"

cd "$(dirname "$0")/.."

# --- preflight ----------------------------------------------------------------
[[ -z "$(git status --porcelain)" ]] || err "working tree not clean — commit or stash first"

CODEVER="$(grep -o 'Version = "[^"]*"' internal/buildinfo/buildinfo.go | cut -d'"' -f2)"
[[ "$CODEVER" == "$VERSION" ]] || err "buildinfo.Version is $CODEVER but releasing $VERSION — keep the lockstep (bump buildinfo + CHANGELOG first)"

grep -q "^## \[$VERSION\]" CHANGELOG.md || err "CHANGELOG.md has no ## [$VERSION] section — write it first"

if gh release view "$TAG" >/dev/null 2>&1; then
    err "release $TAG already exists"
fi

info "Preflight: tests"
go vet ./...
go test ./...

# --- build matrix -------------------------------------------------------------
LDFLAGS="-s -w -X github.com/samdotson61/gpty/internal/buildinfo.Version=${VERSION}"
TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

rm -rf dist && mkdir -p dist
for t in $TARGETS; do
    GOOS="${t%/*}"; GOARCH="${t#*/}"
    name="gpty_${VERSION}_${GOOS}_${GOARCH}"
    stage="dist/$name"
    mkdir -p "$stage"
    ext=""; [[ "$GOOS" == "windows" ]] && ext=".exe"
    info "Building $t"
    GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$stage/gpty$ext" ./cmd/gpty
    GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$stage/gmux$ext" ./cmd/gmux
    cp README.md LICENSE NOTICE "$stage/"
    if [[ "$GOOS" == "windows" ]]; then
        cp install.ps1 install.cmd "$stage/"
        (cd dist && zip -qr "$name.zip" "$name")
    else
        tar -czf "dist/$name.tar.gz" -C dist "$name"
    fi
    rm -rf "$stage"
done

(cd dist && shasum -a 256 ./*.tar.gz ./*.zip > checksums.txt)
info "Artifacts:"; ls -la dist/ | tail -n +2

# --- tag + publish ------------------------------------------------------------
if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    info "Tagging $TAG"
    git tag -a "$TAG" -m "gpty $TAG"
fi
git push origin main "$TAG"

# Release notes = this version's CHANGELOG section.
awk -v ver="$VERSION" '
    $0 ~ "^## \\[" ver "\\]" { grab = 1; next }
    grab && /^## \[/          { exit }
    grab                      { print }
' CHANGELOG.md > dist/notes.md

info "Publishing GitHub release $TAG"
gh release create "$TAG" dist/*.tar.gz dist/*.zip dist/checksums.txt \
    --title "gpty $TAG" --notes-file dist/notes.md

# --- homebrew tap ---------------------------------------------------------------
# Regenerate the formula from the template with this release's real checksums and
# push it, so `brew upgrade gpty` never lags the binaries. Skipped (loudly) if the
# tap repo isn't reachable — the GitHub release is already published by here.
TAP_REPO="${GPTY_TAP_REPO:-samdotson61/homebrew-tap}"
sha_for() { awk -v f="./gpty_${VERSION}_$1.tar.gz" '$2 == f {print $1}' dist/checksums.txt; }

if tapdir=$(mktemp -d) && git clone -q "https://github.com/$TAP_REPO.git" "$tapdir" 2>/dev/null; then
    mkdir -p "$tapdir/Formula"
    sed -e "s|@@VERSION@@|$VERSION|g" \
        -e "s|@@SHA_DARWIN_ARM64@@|$(sha_for darwin_arm64)|" \
        -e "s|@@SHA_DARWIN_AMD64@@|$(sha_for darwin_amd64)|" \
        -e "s|@@SHA_LINUX_ARM64@@|$(sha_for linux_arm64)|" \
        -e "s|@@SHA_LINUX_AMD64@@|$(sha_for linux_amd64)|" \
        packaging/homebrew/gpty.rb.tmpl > "$tapdir/Formula/gpty.rb"
    if [ -n "$(git -C "$tapdir" status --porcelain)" ]; then
        git -C "$tapdir" add Formula/gpty.rb
        git -C "$tapdir" commit -q -m "gpty $VERSION"
        git -C "$tapdir" push -q
        info "Homebrew tap updated: $TAP_REPO -> gpty $VERSION"
    else
        info "Homebrew tap already at $VERSION"
    fi
    rm -rf "$tapdir"
else
    info "WARNING: could not clone $TAP_REPO — formula NOT updated; \`brew\` users stay on the previous version until it is"
fi

info "Done: $(gh release view "$TAG" --json url --jq .url)"
