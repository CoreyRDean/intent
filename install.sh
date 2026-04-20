#!/usr/bin/env bash
# intent installer — pulls the latest release binary for your platform and
# drops it in $PREFIX/bin (default: /usr/local/bin). Symlinks `i` to `intent`.
#
# usage:
#   curl -fsSL https://raw.githubusercontent.com/CoreyRDean/intent/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/CoreyRDean/intent/main/install.sh | bash -s -- --channel nightly
#   curl -fsSL https://raw.githubusercontent.com/CoreyRDean/intent/main/install.sh | bash -s -- --version v0.1.0
#
# environment:
#   PREFIX           install root (default /usr/local; needs sudo if not writable)
#   INTENT_TMPDIR    where to stage downloads (default $TMPDIR or /tmp)
#
# This script does not auto-install the daemon, the model runtime, or the
# model. Run `i init` after install.

set -Eeuo pipefail

REPO="CoreyRDean/intent"
PREFIX="${PREFIX:-/usr/local}"
CHANNEL="stable"
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --channel) CHANNEL="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --prefix)  PREFIX="$2";  shift 2 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

err() { echo "intent install: $*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

have curl || { err "curl is required"; exit 1; }
have tar  || { err "tar is required";  exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin|linux) ;;
  *) err "unsupported os: $OS"; exit 1 ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) err "unsupported arch: $ARCH"; exit 1 ;;
esac

# Resolve version.
if [[ -z "$VERSION" ]]; then
  if [[ "$CHANNEL" == "nightly" ]]; then
    # Pick the latest pre-release with a numeric pre-release suffix.
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=30" \
      | grep -E '"tag_name":' \
      | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' \
      | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]+$' \
      | head -n1 || true)"
  else
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | grep -E '"tag_name":' \
      | head -n1 \
      | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  fi
fi
if [[ -z "$VERSION" ]]; then
  err "could not resolve a version on channel '$CHANNEL'"
  exit 3
fi

ASSET="intent-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

TMP="${INTENT_TMPDIR:-${TMPDIR:-/tmp}}/intent-install-$$"
mkdir -p "$TMP"
trap 'rm -rf "$TMP"' EXIT

echo "downloading ${ASSET} (${VERSION})..."
curl -fL --progress-bar -o "$TMP/$ASSET" "$URL"

echo "verifying checksum..."
SUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"
if curl -fsSL -o "$TMP/SHA256SUMS" "$SUMS_URL" 2>/dev/null; then
  expected="$(grep " ${ASSET}\$" "$TMP/SHA256SUMS" | awk '{print $1}' || true)"
  if [[ -n "$expected" ]]; then
    if have shasum; then
      actual="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    else
      actual="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    fi
    if [[ "$actual" != "$expected" ]]; then
      err "checksum mismatch! expected $expected got $actual"
      exit 4
    fi
    echo "checksum ok."
  else
    err "checksum for ${ASSET} not in SHA256SUMS; refusing to install"
    exit 4
  fi
else
  err "no SHA256SUMS published for ${VERSION}; refusing to install (set INTENT_INSECURE=1 to override)"
  [[ "${INTENT_INSECURE:-0}" = "1" ]] || exit 4
fi

echo "extracting..."
tar -C "$TMP" -xzf "$TMP/$ASSET"
BIN_FILE="$TMP/intent-${OS}-${ARCH}"
[[ -x "$BIN_FILE" ]] || chmod +x "$BIN_FILE"

DEST="${PREFIX}/bin/intent"
LINK="${PREFIX}/bin/i"
mkdir -p "${PREFIX}/bin"

if [[ -w "${PREFIX}/bin" ]]; then
  install -m 0755 "$BIN_FILE" "$DEST"
  ln -sf intent "$LINK"
else
  echo "elevating to install to $PREFIX (sudo)..."
  sudo install -m 0755 "$BIN_FILE" "$DEST"
  sudo ln -sf intent "$LINK"
fi

echo
echo "installed:"
echo "  $DEST"
echo "  $LINK"
echo
echo "next:"
echo "  i init             # first-run setup"
echo "  i model pull       # download the local model (~4.7 GB)"
echo "  i hello            # try it"
