#!/bin/bash
set -e

REPO="jneb802/mmcli"
INSTALL_DIR="/usr/local/bin"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  arm64) BINARY="mmcli-darwin-arm64" ;;
  x86_64) BINARY="mmcli-darwin-amd64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest release tag
echo "Fetching latest release..."
TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$TAG" ]; then
  echo "Failed to fetch latest release."
  exit 1
fi
echo "Latest version: $TAG"

# Download
URL="https://github.com/$REPO/releases/download/$TAG/$BINARY"
TMPFILE=$(mktemp)
echo "Downloading $BINARY..."
curl -fsSL -o "$TMPFILE" "$URL"

# Install
chmod +x "$TMPFILE"
echo "Installing to $INSTALL_DIR/mmcli (may require sudo)..."
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMPFILE" "$INSTALL_DIR/mmcli"
else
  sudo mv "$TMPFILE" "$INSTALL_DIR/mmcli"
fi

echo "mmcli installed successfully! Run 'mmcli init' to get started."
