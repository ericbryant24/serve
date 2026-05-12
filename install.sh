#!/bin/sh
set -e

REPO="ericbryant24/serve"
BIN="serve"
INSTALL_DIR="${HOME}/.local/bin"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

# Detect arch
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)       ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" && exit 1 ;;
esac

# Get latest release tag
VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": *"\(.*\)".*/\1/')

if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest release version" && exit 1
fi

VERSION_NUM="${VERSION#v}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${BIN}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"

echo "Downloading ${BIN} ${VERSION} (${OS}/${ARCH})..."
TMP=$(mktemp -d)
curl -sSL "$URL" | tar -xz -C "$TMP"

mkdir -p "$INSTALL_DIR"
mv "$TMP/${BIN}" "${INSTALL_DIR}/${BIN}"
chmod +x "${INSTALL_DIR}/${BIN}"
rm -rf "$TMP"

echo "Installed to ${INSTALL_DIR}/${BIN}"

# Warn if install dir is not in PATH
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *) echo "Note: add ${INSTALL_DIR} to your PATH to use ${BIN} from anywhere" ;;
esac
