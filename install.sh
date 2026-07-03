#!/bin/sh
# reponite installer — downloads the latest prebuilt binary for your OS/arch.
#
#   curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
#
# Override the install dir with REPONITE_BINDIR (default /usr/local/bin).
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

asset="reponite-${goos}-${goarch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"
tmp="$(mktemp)"
echo "reponite: downloading ${asset} ..."
if ! curl -fSL "$url" -o "$tmp"; then
  echo "reponite: no prebuilt binary for ${goos}/${goarch} (or no release yet). Build from source: make cli"
  rm -f "$tmp"; exit 1
fi
chmod +x "$tmp"

if [ -w "$BINDIR" ]; then
  mv "$tmp" "$BINDIR/reponite"
else
  echo "reponite: installing to $BINDIR (sudo) ..."
  sudo mv "$tmp" "$BINDIR/reponite"
fi

echo "reponite: installed at $(command -v reponite)"
reponite version || true
echo
echo "Next:"
echo "  reponite index /path/to/repo     # build the index"
echo "  reponite setup  /path/to/repo     # register it with your agent (or install the Cowork plugin)"
