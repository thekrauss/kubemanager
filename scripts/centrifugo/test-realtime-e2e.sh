#!/usr/bin/env bash
# Test end-to-end de la chaîne Centrifugo :
#   1. Génère un JWT connection (HS256, secret partagé avec Centrifugo)
#   2. Génère un JWT subscription scopé à un canal
#   3. Ouvre une WebSocket via websocat
#   4. Subscribe au canal
#   5. Publie un event via l'API HTTP Centrifugo
#   6. Vérifie que l'event est bien reçu côté subscriber
#
# Sortie non-zéro si la boucle complète ne marche pas.
#
# Usage :
#   bash scripts/centrifugo/test-realtime-e2e.sh

set -euo pipefail

# ─── Config (source .env) ─────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [ -f "${REPO_ROOT}/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "${REPO_ROOT}/.env"
  set +a
fi

CENTRIFUGO_URL="${CENTRIFUGO_URL:-http://127.0.0.1:8000}"
CENTRIFUGO_WS="${CENTRIFUGO_WS:-ws://127.0.0.1:8000/connection/websocket}"
API_KEY="${CENTRIFUGO_API_KEY:?CENTRIFUGO_API_KEY required}"
HMAC_SECRET="${CENTRIFUGO_TOKEN_HMAC_SECRET:?CENTRIFUGO_TOKEN_HMAC_SECRET required}"

CHANNEL="${CHANNEL:-project:00000000-0000-0000-0000-000000000001:workload:wk-abc:deploy}"
USER_SUB="${USER_SUB:-test-user-$(date +%s)}"

# ─── Helpers ──────────────────────────────────────────────────────────────
log()  { printf "\033[36m==>\033[0m %s\n" "$*" >&2; }
fail() { printf "\033[31mFAIL:\033[0m %s\n" "$*" >&2; exit 1; }
ok()   { printf "\033[32mOK:\033[0m %s\n" "$*" >&2; }

require_bin() { command -v "$1" >/dev/null 2>&1 || fail "binaire requis: $1"; }

# base64url (no padding) — format JWT.
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# sign_jwt_hs256 <json_payload> <secret>
sign_jwt_hs256() {
  local payload="$1" secret="$2"
  local header='{"alg":"HS256","typ":"JWT"}'
  local h p sig
  h=$(printf '%s' "$header"  | b64url)
  p=$(printf '%s' "$payload" | b64url)
  sig=$(printf '%s' "$h.$p" | openssl dgst -binary -sha256 -hmac "$secret" | b64url)
  printf '%s.%s.%s' "$h" "$p" "$sig"
}

# ─── Pre-flight ───────────────────────────────────────────────────────────
require_bin openssl
require_bin websocat
require_bin curl
require_bin jq

log "Ping Centrifugo ${CENTRIFUGO_URL}"
if ! curl -fsS -X POST "${CENTRIFUGO_URL}/api/info" \
     -H "X-API-Key: ${API_KEY}" -d '{}' >/dev/null; then
  fail "Centrifugo not reachable (ou API key invalide) — as-tu lancé 'make dev-up' ?"
fi
ok "Centrifugo reachable"

# ─── 1. Génère les JWTs ───────────────────────────────────────────────────
NOW=$(date +%s)
EXP=$((NOW + 300)) # 5 min

CONNECT_PAYLOAD=$(jq -cn --arg sub "$USER_SUB" --argjson exp "$EXP" \
  '{sub:$sub, exp:$exp}')
CONNECT_TOKEN=$(sign_jwt_hs256 "$CONNECT_PAYLOAD" "$HMAC_SECRET")
log "Connection JWT sub=${USER_SUB} exp=${EXP}"

SUB_PAYLOAD=$(jq -cn --arg sub "$USER_SUB" --arg ch "$CHANNEL" --argjson exp "$EXP" \
  '{sub:$sub, channel:$ch, exp:$exp}')
SUB_TOKEN=$(sign_jwt_hs256 "$SUB_PAYLOAD" "$HMAC_SECRET")
log "Subscription JWT channel=${CHANNEL}"

# ─── 2. Ouvre la WS + subscribe ───────────────────────────────────────────
# websocat en mode async : on écrit des commandes JSON sur stdin et on lit
# les réponses sur stdout. Chaque message Centrifugo = 1 ligne JSON.

WS_IN=$(mktemp -u /tmp/ws-in.XXXX)
WS_OUT=$(mktemp /tmp/ws-out.XXXX)
mkfifo "$WS_IN"

# Lance websocat en background, stdin via fifo.
websocat --text --no-close "$CENTRIFUGO_WS" < "$WS_IN" > "$WS_OUT" 2>&1 &
WS_PID=$!
# Garde le fifo ouvert en écriture.
exec 9>"$WS_IN"

cleanup() {
  exec 9>&- 2>/dev/null || true
  kill "$WS_PID" 2>/dev/null || true
  rm -f "$WS_IN" "$WS_OUT"
}
trap cleanup EXIT

sleep 0.3

# Envoie connect (id=1)
CONNECT_MSG=$(jq -cn --arg t "$CONNECT_TOKEN" \
  '{id:1, connect:{token:$t, name:"e2e-test"}}')
echo "$CONNECT_MSG" >&9

# Envoie subscribe (id=2)
SUB_MSG=$(jq -cn --arg ch "$CHANNEL" --arg t "$SUB_TOKEN" \
  '{id:2, subscribe:{channel:$ch, token:$t}}')
echo "$SUB_MSG" >&9

# Laisse Centrifugo répondre (connect OK + subscribe OK).
sleep 0.5

# Vérifie qu'on a bien reçu les 2 réponses initiales.
if ! grep -q '"id":1' "$WS_OUT"; then
  fail "pas de réponse au connect — dump:\n$(cat "$WS_OUT")"
fi
if ! grep -q '"id":2' "$WS_OUT"; then
  fail "pas de réponse au subscribe — dump:\n$(cat "$WS_OUT")"
fi
ok "connect + subscribe acceptés par Centrifugo"

# ─── 3. Publie un event via l'API HTTP ─────────────────────────────────────
MARKER="e2e-$(date +%s%N)"
PUB_PAYLOAD=$(jq -cn --arg ch "$CHANNEL" --arg m "$MARKER" \
  '{channel:$ch, data:{test:"e2e-realtime", marker:$m, phase:"HELM_INSTALLING"}}')

log "Publish event marker=${MARKER}"
PUB_RESP=$(curl -fsS -X POST "${CENTRIFUGO_URL}/api/publish" \
  -H "X-API-Key: ${API_KEY}" -H "Content-Type: application/json" \
  -d "$PUB_PAYLOAD")
OFFSET=$(echo "$PUB_RESP" | jq -r '.result.offset // empty')
[ -n "$OFFSET" ] || fail "publish failed: $PUB_RESP"
ok "publish accepté (offset=${OFFSET})"

# ─── 4. Vérifie réception sur le subscriber ────────────────────────────────
# Centrifugo envoie des publications en format "push" :
#   {"push":{"channel":"...","pub":{"data":{...},"offset":N}}}
# On attend jusqu'à 3s l'apparition du marker dans le flux WS.

log "Attends réception côté subscriber..."
for i in $(seq 1 15); do
  if grep -q "$MARKER" "$WS_OUT"; then
    ok "event reçu par le subscriber ✅"
    echo
    echo "── Message intercepté ──"
    grep "$MARKER" "$WS_OUT" | head -1 | jq .
    exit 0
  fi
  sleep 0.2
done

echo "── Dump WS_OUT complet ──" >&2
cat "$WS_OUT" >&2
fail "event non reçu dans la fenêtre de 3s"
