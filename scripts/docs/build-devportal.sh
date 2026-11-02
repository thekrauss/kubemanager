#!/usr/bin/env bash
set -euo pipefail

# Serves the KubeManager DevPortal locally.
# Usage:
#   scripts/docs/build-devportal.sh           # serve on :8090
#   scripts/docs/build-devportal.sh 8091      # custom port

PORT="${1:-8090}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DOCS="$ROOT/docs"

if [[ ! -f "$DOCS/devportal.html" ]]; then
  echo "error: $DOCS/devportal.html not found" >&2
  exit 1
fi

echo "serving devportal at http://localhost:${PORT}/devportal.html"
echo "  (Ctrl-C to stop)"
cd "$DOCS"
exec python3 -m http.server "$PORT"
