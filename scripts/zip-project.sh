#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

NAME="edgeflux-$(date +%Y%m%d-%H%M%S).zip"
OUT="${1:-$HOME/Desktop/$NAME}"

zip -r "$OUT" . \
  -x ".git/*" \
  -x ".DS_Store" \
  -x "*/.DS_Store" \
  -x "certs/*.pem" \
  -x "certs/*.key" \
  -x "certs/*.csr" \
  -x "certs/*.srl" \
  -x "certs/*.crt" \
  -x "tmp/*"

echo ""
echo "Created: $OUT ($(du -h "$OUT" | cut -f1))"
