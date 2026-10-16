#!/bin/sh
# reponite installer — downloads the latest release binary for your OS/arch.
#
#   curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
#
# REPONITE_BINDIR   install directory (default /usr/local/bin)
# REPONITE_VERSION  a release tag such as v0.3.0 (default: the latest release)
set -e

REPO="vishwak02/reponite"
BINDIR="${REPONITE_BINDIR:-/usr/local/bin}"

os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux)  goos=linux ;;
  Darwin) goos=darwin ;;
  *) echo "reponite: unsupported OS '$os' — build from source with 'make cli'"; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64)  goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) echo "reponite: unsupported arch '$arch' — build from source with 'make cli'"; exit 1 ;;
esac

if [ -n "$REPONITE_VERSION" ]; then
  base="https://github.com/${REPO}/releases/download/${REPONITE_VERSION}"
else
  base="https://github.com/${REPO}/releases/latest/download"
fi
asset="reponite-${goos}-${goarch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "reponite: downloading ${asset} ..."
if ! curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}"; then
  echo "reponite: no release binary for ${goos}/${goarch}. Build from source: make cli"
  exit 1
fi

# Verify against the release's SHA256SUMS when it publishes one.
if curl -fsSL "${base}/SHA256SUMS" -o "${tmp}/SHA256SUMS" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    sum() { sha256sum "$1" | cut -d' ' -f1; }
  else
    sum() { shasum -a 256 "$1" | cut -d' ' -f1; }
  fi
  want=$(grep " ${asset}\$" "${tmp}/SHA256SUMS" | cut -d' ' -f1)
  got=$(sum "${tmp}/${asset}")
  if [ -z "$want" ] || [ "$want" != "$got" ]; then
    echo "reponite: checksum mismatch for ${asset} — not installing"
    exit 1
  fi
  echo "reponite: checksum verified"
else
  echo "reponite: this release has no SHA256SUMS; installing without a checksum check"
fi
chmod +x "${tmp}/${asset}"

if [ -w "$BINDIR" ] || { [ ! -e "$BINDIR" ] && mkdir -p "$BINDIR" 2>/dev/null; }; then
  mv "${tmp}/${asset}" "$BINDIR/reponite"
else
  echo "reponite: installing to $BINDIR (sudo) ..."
  sudo mv "${tmp}/${asset}" "$BINDIR/reponite"
fi

echo "reponite: installed at $BINDIR/reponite"
"$BINDIR/reponite" version || true
case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *) echo "reponite: $BINDIR is not on your PATH — add it, or run $BINDIR/reponite" ;;
esac
echo
echo "Next:"
echo "  reponite index /path/to/repo     # build the index"
echo "  reponite setup /path/to/repo     # register it with your AI agent"
echo "  docs: https://github.com/${REPO}/blob/main/docs/USER_GUIDE.md"
