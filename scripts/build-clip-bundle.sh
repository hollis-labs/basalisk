#!/usr/bin/env bash
# Build frontend/public/clip/extract.js: fe-clipper's page extraction (Readability, turndown and
# its site adapters) as one classic script, which Frag's "Clip" runs inside a Browser window's
# page. Never edit the output by hand; change the pin and run this again.
#
#   scripts/build-clip-bundle.sh                 # from the fe-clipper repository on GitHub
#   FE_CLIPPER_SRC=/path/to/fe-clipper scripts/build-clip-bundle.sh   # from a local clone
#
# The source is cloned into a temporary directory at the pinned commit; nothing is changed in it.
set -euo pipefail

PIN="beb449a270e061d5fc1c0df6e00997542fd49cf8"
SRC="${FE_CLIPPER_SRC:-https://github.com/hollis-labs/fe-clipper.git}"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/frontend/public/clip/extract.js"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git clone --quiet --no-checkout "$SRC" "$work/fe-clipper"
git -C "$work/fe-clipper" checkout --quiet "$PIN"
(cd "$work/fe-clipper" && npm ci --silent --ignore-scripts --no-audit --no-fund)
(cd "$work/fe-clipper" && ./node_modules/.bin/esbuild src/content/index.ts --bundle --format=iife \
  --target=safari16 --minify --legal-comments=eof --outfile="$work/extract.js" --log-level=warning)

mkdir -p "$(dirname "$out")"
{
  echo "/* fe-clipper page extraction, built by scripts/build-clip-bundle.sh from fe-clipper $PIN."
  echo " * Readability (Apache-2.0) and turndown (MIT): see licenses/. Do not edit; rebuild. */"
  cat "$work/extract.js"
} >"$out"
echo "built $out ($(wc -c <"$out" | tr -d ' ') bytes) from fe-clipper $PIN"
