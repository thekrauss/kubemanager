#!/usr/bin/env bash
# scripts/e2e.sh — smoke e2e against a running dev stack.
#
# Prerequisites (all via `make dev-up`):
#   - kubemanager backend on :8080
#   - keycloak on :18081
#   - postgres, redis, temporal, centrifugo up
#   - at least one cluster enrolled (for workload deploys to succeed) — optional
#
# Scenario :
#   1. login (ROPC) → Bearer token
#   2. create project → poll until READY
#   3. deploy workload → poll until RUNNING
#   4. stop workload → assert STOPPED
#   5. start workload → assert RUNNING
#   6. delete workload → poll until DELETED
#   7. delete project → poll until completion
#
# Usage :
#   bash scripts/e2e.sh
#   KUBEMANAGER_URL=http://localhost:8080 bash scripts/e2e.sh
#   SKIP_WORKLOAD=1 bash scripts/e2e.sh   # project-only smoke
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

KUBEMANAGER_URL="${KUBEMANAGER_URL:-http://localhost:8080}"
API_PREFIX="${API_PREFIX:-/kmanager/v1}"
TIMEOUT_PROJECT="${TIMEOUT_PROJECT:-120}"   # seconds to reach READY
TIMEOUT_WORKLOAD="${TIMEOUT_WORKLOAD:-300}" # seconds to reach RUNNING
TIMEOUT_DELETE="${TIMEOUT_DELETE:-120}"

PROJECT_NAME="e2e-$(date +%s)"
WORKLOAD_NAME="api-$(date +%s)"
IMAGE="${E2E_IMAGE:-nginx:alpine}"

# ── Helpers ─────────────────────────────────────────────────────────────────

log()  { printf '\033[1;36m[%s]\033[0m %s\n' "$(date +%H:%M:%S)" "$*"; }
ok()   { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || fail "missing dep: $1"
}
require curl
require jq

wait_health() {
  log "waiting for kubemanager ${KUBEMANAGER_URL}/api/v1/health ..."
  for i in $(seq 1 30); do
    if curl -sf "${KUBEMANAGER_URL}/api/v1/health" >/dev/null 2>&1; then
      ok "kubemanager healthy"
      return
    fi
    sleep 1
  done
  fail "kubemanager did not become healthy"
}

obtain_token() {
  log "obtaining bearer token via scripts/keycloak/get_test_token.sh ..."
  TOKEN="$(bash "${SCRIPT_DIR}/keycloak/get_test_token.sh" 2>/dev/null || true)"
  [[ -n "${TOKEN:-}" ]] || fail "could not obtain token (is Keycloak up?)"
  ok "token acquired (${#TOKEN} chars)"
}

api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS -X "$method" -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json")
  [[ -n "$body" ]] && args+=(--data "$body")
  curl "${args[@]}" "${KUBEMANAGER_URL}${API_PREFIX}${path}"
}

wait_project_status() {
  local id="$1" target="$2" timeout="$3"
  local end=$(( $(date +%s) + timeout ))
  while [[ $(date +%s) -lt $end ]]; do
    local status
    status="$(api GET "/projects/${id}/status" | jq -r '.status // empty')"
    log "project ${id} status=${status}"
    [[ "$status" == "$target" ]] && return
    [[ "$status" == "FAILED" ]] && fail "project ${id} entered FAILED state"
    sleep 3
  done
  fail "project ${id} did not reach ${target} within ${timeout}s"
}

wait_workload_status() {
  local id="$1" target="$2" timeout="$3"
  local end=$(( $(date +%s) + timeout ))
  while [[ $(date +%s) -lt $end ]]; do
    local status
    status="$(api GET "/workloads/${id}/status" | jq -r '.status // empty')"
    log "workload ${id} status=${status}"
    [[ "$status" == "$target" ]] && return
    [[ "$status" == "FAILED" ]] || [[ "$status" == "DELETE_FAILED" ]] && fail "workload ${id} in ${status} state"
    sleep 3
  done
  fail "workload ${id} did not reach ${target} within ${timeout}s"
}

wait_workload_gone() {
  local id="$1" timeout="$2"
  local end=$(( $(date +%s) + timeout ))
  while [[ $(date +%s) -lt $end ]]; do
    local code
    code="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${TOKEN}" "${KUBEMANAGER_URL}${API_PREFIX}/workloads/${id}")"
    log "workload ${id} GET → HTTP ${code}"
    [[ "$code" == "404" ]] && return
    sleep 3
  done
  fail "workload ${id} still present after ${timeout}s"
}

# ── Scenario ────────────────────────────────────────────────────────────────

main() {
  wait_health
  obtain_token

  log "1. create project name=${PROJECT_NAME}"
  local project_resp project_id wf_id
  project_resp="$(api POST "/projects" "$(jq -n --arg n "$PROJECT_NAME" --arg d "e2e smoke" --arg c "2000m" --arg m "2Gi" '{name:$n, description:$d, cpu_limit:$c, memory_limit:$m}')")"
  echo "$project_resp" | jq -r '.'
  project_id="$(echo "$project_resp" | jq -r '.project_id // empty')"
  wf_id="$(echo "$project_resp" | jq -r '.workflow_id // empty')"
  [[ -n "$project_id" ]] || project_id="$(echo "$project_resp" | jq -r '.id // empty')"
  [[ -n "$project_id" ]] || fail "no project_id in response"
  ok "project created id=${project_id} workflow=${wf_id}"

  wait_project_status "$project_id" "READY" "$TIMEOUT_PROJECT"
  ok "project READY"

  if [[ "${SKIP_WORKLOAD:-0}" == "1" ]]; then
    log "SKIP_WORKLOAD=1 → skipping workload phase"
  else
    log "2. deploy workload name=${WORKLOAD_NAME} image=${IMAGE}"
    local wl_resp wl_id
    wl_resp="$(api POST "/workloads" "$(jq -n \
      --arg p "$project_id" --arg n "$WORKLOAD_NAME" --arg i "$IMAGE" \
      '{project_id:$p, name:$n, image:$i, replicas:1, cpu_limit:"200m", memory_limit:"256Mi", target_port:80}')")"
    echo "$wl_resp" | jq -r '.'
    wl_id="$(echo "$wl_resp" | jq -r '.workload_id // .id // empty')"
    [[ -n "$wl_id" ]] || fail "no workload_id in response"
    ok "workload submitted id=${wl_id}"

    wait_workload_status "$wl_id" "RUNNING" "$TIMEOUT_WORKLOAD"
    ok "workload RUNNING"

    log "3. stop workload"
    api POST "/workloads/${wl_id}/stop" >/dev/null
    wait_workload_status "$wl_id" "STOPPED" 60
    ok "workload STOPPED"

    log "4. start workload"
    api POST "/workloads/${wl_id}/start" >/dev/null
    wait_workload_status "$wl_id" "RUNNING" 60
    ok "workload RUNNING (restart)"

    log "5. delete workload"
    api DELETE "/workloads/${wl_id}" >/dev/null
    wait_workload_gone "$wl_id" "$TIMEOUT_DELETE"
    ok "workload gone"
  fi

  log "6. delete project"
  api DELETE "/projects/${project_id}" >/dev/null
  ok "project delete initiated"

  ok "e2e smoke passed"
}

main "$@"
