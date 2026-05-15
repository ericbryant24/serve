#!/bin/sh
set -e

DEST="$HOME/Library/Services/Serve.workflow"
SRC="$(cd "$(dirname "$0")" && pwd)/Serve.workflow"

rm -rf "$DEST"
cp -r "$SRC" "$DEST"
/System/Library/CoreServices/pbs -update

echo "Installed. Right-click a file or folder in Finder → Quick Actions → Serve."
