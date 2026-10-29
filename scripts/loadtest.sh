#!/usr/bin/env bash
# scripts/loadtest.sh — runner wrapper for loadtest/k6/scenarios.js.
#
# Bootstraps a project + a workload (if not provided), then runs k6 with
# thresholds. Results are stored under loadtest/results/<timestamp>.json.
#
# Env:
#   BASE_URL       default http://localhost:8080
#   SCENARIO       all | read_workloads | realtime_token | create_workload
#   PROJECT_ID     skip bootstrap if set
#   WORKLOAD_ID    skip bootstrap if set
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BASE_URL="${BASE_URL:-http://localhost:8080}"
API_PREFIX="${API_PREFIX:-/kmanager/v1}"
SCENARIO="${SCENARIO:-all}"

command -v k6   >/dev/null || { echo "install k6: https://k6.io/docs/get-started/installation/" >&2; exit 1; }
command -v jq   >/dev/null || { echo "install jq" >&2; exit 1; }
command -v curl >/dev/null || { echo "install curl" >&2; exit 1; }

echo "[loadtest] fetching token..."
TOKEN="$(bash "${SCRIPT_DIR}/keycloak/get_test_token.sh")"

# Bootstrap project/workload if not provided.
if [[ -z "${PROJECT_ID:-}" ]]; then
  echo "[loadtest] creating project..."
  PROJECT_ID="$(curl -sS -X POST -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
    "${BASE_URL}${API_PREFIX}/projects" \
    -d '{"name":"loadtest-'$(date +%s)'","description":"k6","cpu_limit":"10000m","memory_limit":"32Gi"}' \
    | jq -r '.project_id // .id')"
  echo "[loadtest] project_id=${PROJECT_ID}"
  sleep 10 # let the workflow settle
fi

if [[ -z "${WORKLOAD_ID:-}" ]]; then
  echo "[loadtest] creating seed workload..."
  WORKLOAD_ID="$(curl -sS -X POST -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
    "${BASE_URL}${API_PREFIX}/workloads" \
    -d "$(jq -n --arg p "$PROJECT_ID" '{project_id:$p,name:"k6-seed",image:"nginx:alpine",replicas:1,cpu_limit:"100m",memory_limit:"128Mi",target_port:80}')" \
    | jq -r '.workload_id // .id')"
  echo "[loadtest] workload_id=${WORKLOAD_ID}"
  sleep 5
fi

mkdir -p "${REPO_ROOT}/loadtest/results"
OUT="${REPO_ROOT}/loadtest/results/$(date +%s).json"

export BASE_URL API_PREFIX TOKEN PROJECT_ID WORKLOAD_ID SCENARIO

echo "[loadtest] running scenario=${SCENARIO}"
k6 run --out "json=${OUT}" "${REPO_ROOT}/loadtest/k6/scenarios.js"

echo "[loadtest] results: ${OUT}"
