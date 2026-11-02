#!/usr/bin/env bash
# Binaires de la CLI betorun pour les utilisateurs (Linux, macOS, Windows ;
# amd64 et arm64), avec leurs sommes SHA-256.
#
#   VERSION=v0.1.0 API_URL=https://api.example.com/kmanager/v1 bash scripts/build_cli.sh
#
# API_URL : API utilisée par défaut (sinon `betorun login --api <url>`).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
API_URL="${API_URL:-}"
OUT="${OUT:-dist/cli}"
PKG="github.com/thekrauss/kubemanager/internal/cli"

rm -rf "$OUT" && mkdir -p "$OUT"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}" arch="${target#*/}"
  bin="betorun_${VERSION}_${os}_${arch}"
  [ "$os" = windows ] && bin="${bin}.exe"
  echo "==> ${bin}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.DefaultAPIURL=${API_URL}" \
    -o "${OUT}/${bin}" ./cmd/betorun
done
(cd "$OUT" && sha256sum betorun_* > SHA256SUMS)
echo "==> ${OUT}/SHA256SUMS"
