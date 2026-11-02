// KubeManager — k6 load scenarios.
//
// Runs in phases:
//   A. read_workloads  — GET /workloads/:id     (hot cache read path)
//   B. realtime_token  — GET /realtime/token    (HMAC sign + authz)
//   C. create_workload — POST /workloads        (DB write + Temporal submit)
//
// Usage:
//   BASE_URL=http://localhost:8080 \
//   TOKEN=$(bash scripts/keycloak/get_test_token.sh) \
//   PROJECT_ID=... \
//   WORKLOAD_ID=... \
//   k6 run loadtest/k6/scenarios.js
//
//   # Single scenario:
//   k6 run -e SCENARIO=read_workloads loadtest/k6/scenarios.js
//
// Output:
//   k6 run --out json=loadtest/results/$(date +%s).json loadtest/k6/scenarios.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';

const BASE_URL    = __ENV.BASE_URL    || 'http://localhost:8080';
const API_PREFIX  = __ENV.API_PREFIX  || '/kmanager/v1';
const TOKEN       = __ENV.TOKEN       || '';
const PROJECT_ID  = __ENV.PROJECT_ID  || '';
const WORKLOAD_ID = __ENV.WORKLOAD_ID || '';
const SCENARIO    = __ENV.SCENARIO    || 'all';

const readLatency    = new Trend('read_workload_latency', true);
const tokenLatency   = new Trend('realtime_token_latency', true);
const createLatency  = new Trend('create_workload_latency', true);
const nonOK          = new Rate('non_ok_responses');

// ── Options — scenarios + thresholds (SLO) ─────────────────────────────────

export const options = {
  scenarios: pickScenarios(SCENARIO),
  thresholds: {
    // Global : <1% erreurs, P95 < 500ms par défaut.
    'http_req_failed':  [{ threshold: 'rate<0.01', abortOnFail: true }],
    'http_req_duration': ['p(95)<500'],

    // Per-endpoint P95 SLOs (tighter for reads).
    'read_workload_latency':    ['p(95)<150'],
    'realtime_token_latency':   ['p(95)<200'],
    'create_workload_latency':  ['p(95)<800'],
    'non_ok_responses':         ['rate<0.01'],
  },
};

function pickScenarios(name) {
  const all = {
    read_workloads: {
      executor: 'ramping-arrival-rate',
      exec:     'readWorkloads',
      startRate: 10,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 200,
      stages: [
        { target: 50,  duration: '30s' },
        { target: 200, duration: '1m' },
        { target: 200, duration: '1m' },
        { target: 0,   duration: '15s' },
      ],
    },
    realtime_token: {
      executor: 'constant-arrival-rate',
      exec:     'realtimeToken',
      rate: 100, timeUnit: '1s',
      duration: '1m',
      preAllocatedVUs: 50,
      maxVUs: 150,
    },
    create_workload: {
      executor: 'ramping-vus',
      exec:     'createWorkload',
      startVUs: 2,
      stages: [
        { target: 5,  duration: '30s' },
        { target: 10, duration: '1m' },
        { target: 0,  duration: '15s' },
      ],
    },
  };

  if (name === 'all') return all;
  if (all[name])      return { [name]: all[name] };
  throw new Error(`unknown scenario: ${name}`);
}

// ── Helpers ────────────────────────────────────────────────────────────────

function authHeaders() {
  return { headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' } };
}

export function setup() {
  if (!TOKEN)     throw new Error('TOKEN env var is required');
  if (!PROJECT_ID) console.warn('PROJECT_ID not set — create_workload will be skipped');
}

// ── Scenario implementations ───────────────────────────────────────────────

export function readWorkloads() {
  if (!WORKLOAD_ID) return;
  const url = `${BASE_URL}${API_PREFIX}/workloads/${WORKLOAD_ID}`;
  const res = http.get(url, authHeaders());
  readLatency.add(res.timings.duration);
  nonOK.add(res.status !== 200);
  check(res, { '200 OK': (r) => r.status === 200 });
  sleep(0.1);
}

export function realtimeToken() {
  const url = `${BASE_URL}${API_PREFIX}/realtime/token`;
  const res = http.get(url, authHeaders());
  tokenLatency.add(res.timings.duration);
  nonOK.add(res.status !== 200);
  check(res, {
    '200 OK':      (r) => r.status === 200,
    'has token':   (r) => !!(r.json() && r.json().token),
  });
  sleep(0.1);
}

export function createWorkload() {
  if (!PROJECT_ID) return;
  const url = `${BASE_URL}${API_PREFIX}/workloads`;
  const payload = JSON.stringify({
    project_id:   PROJECT_ID,
    name:         `k6-${__VU}-${__ITER}-${Date.now()}`,
    image:        'nginx:alpine',
    replicas:     1,
    cpu_limit:    '100m',
    memory_limit: '128Mi',
    target_port:  80,
  });
  const res = http.post(url, payload, authHeaders());
  createLatency.add(res.timings.duration);
  nonOK.add(res.status >= 400);
  check(res, {
    '202 accepted': (r) => r.status === 202 || r.status === 201,
    'has workload_id': (r) => {
      try { return !!(r.json() && (r.json().workload_id || r.json().id)); }
      catch (_) { return false; }
    },
  });
  sleep(1);
}
