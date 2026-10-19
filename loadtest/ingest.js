// k6 load scenario for the EventFlow ingestion endpoint (spec §29).
//
// Prereq: create a project + API key and a subscription, then export them:
//
//   export EF_API=http://localhost:8080
//   export EF_API_KEY=ef_live_...
//
// Run:
//
//   k6 run --vus=50 --duration=30s loadtest/ingest.js
//
// Target ≈1000 events/sec. Watch requests/sec, p50/p95/p99 and error rate.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const API = __ENV.EF_API || 'http://localhost:8080';
const API_KEY = __ENV.EF_API_KEY || '';

export const options = {
  vus: 50,
  duration: '30s',
  thresholds: {
    http_req_failed: ['rate<0.01'],       // <1% errors
    http_req_duration: ['p(95)<500'],     // p95 under 500ms
  },
};

let counter = 0;

export default function () {
  counter += 1;
  const payload = JSON.stringify({
    type: 'load.test',
    source: 'k6',
    subject: `load:${counter}`,
    data: { seq: counter, order_id: randomIntBetween(1, 100000) },
  });

  const res = http.post(`${API}/api/v1/events`, payload, {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${API_KEY}`,
      // Unique key per request so ingestion is not deduplicated.
      'Idempotency-Key': `k6-${__VU}-${__ITER}-${counter}`,
    },
  });

  check(res, {
    'accepted': (r) => r.status === 202,
    'no server error': (r) => r.status < 500,
  });

  sleep(0.01);
}