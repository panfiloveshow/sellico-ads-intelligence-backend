#!/bin/bash
# Generate PNG icons from SVG logo
# Requires: ImageMagick (brew install imagemagick)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SVG_FILE="$SCRIPT_DIR/logo.svg"
ICON_16="$SCRIPT_DIR/icon16.png"
ICON_48="$SCRIPT_DIR/icon48.png"
ICON_128="$SCRIPT_DIR/icon128.png"

if ! command -v convert &> /dev/null; then
    echo "Error: ImageMagick 'convert' not found."
    echo "Install with: brew install imagemagick"
    exit 1
fi

echo "Generating icons from $SVG_FILE..."

convert -background none -resize 16x16 "$SVG_FILE" "$ICON_16"
echo "✓ icon16.png"

convert -background none -resize 48x48 "$SVG_FILE" "$ICON_48"
echo "✓ icon48.png"

convert -background none -resize 128x128 "$SVG_FILE" "$ICON_128"
echo "✓ icon128.png"

echo "All icons generated successfully!"
