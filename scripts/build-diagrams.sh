#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIAGRAMS_DIR="$REPO_ROOT/diagrams"
IMAGES_DIR="$REPO_ROOT/images"
EXPORT_DIR="$DIAGRAMS_DIR/export"
IMAGE="rlespinasse/drawio-export"

if [ ! -d "$DIAGRAMS_DIR" ]; then
  echo "No diagrams/ directory found." >&2
  exit 1
fi

files=$(find "$DIAGRAMS_DIR" -maxdepth 1 -name '*.drawio' -type f)
if [ -z "$files" ]; then
  echo "No .drawio files found in diagrams/."
  exit 0
fi

mkdir -p "$IMAGES_DIR"

echo "Building diagrams..."
docker run --rm \
  -u "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -v "$DIAGRAMS_DIR:/data" \
  "$IMAGE" \
  --format png \
  --scale 2

# Move generated PNGs to images/
find "$EXPORT_DIR" -name '*.png' -type f | while read -r png; do
  name="$(basename "$png")"
  mv "$png" "$IMAGES_DIR/$name"
  echo "  -> images/$name"
done

# Clean up export directory
rmdir "$EXPORT_DIR" 2>/dev/null || true

echo "Done. Output in images/."
