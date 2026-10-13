#!/bin/bash
# Draws the overview of every example directory into its overview.svg.
# APIEXPORT_PIE=path uses that binary; otherwise the tool is built from this checkout, so the
# pictures always match the current source.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(dirname "$here")

command -v d2 >/dev/null || { echo "d2 is not on PATH: https://d2lang.com" >&2; exit 1; }

pie=${APIEXPORT_PIE:-}
if [ -z "$pie" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  pie=$tmp/apiexport-pie
  (cd "$root" && go build -o "$pie" .)
fi

for dir in "$here"/*/; do
  dir=${dir%/}
  echo "== examples/$(basename "$dir")" >&2
  "$pie" --style overview -o "$dir/overview.svg" "$dir"
done
