import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080/api/v1';

export const options = {
  scenarios: {

    // Target: 1,000 TPS for the blog list API.
    read_blog_scenario: {
      executor: 'ramping-arrival-rate',
      startRate: 1000,
      timeUnit: '1s',
      preAllocatedVUs: 1500,  // Keep enough preallocated VUs for the target rate.
      maxVUs: 10000,
      stages: [
        { target: 500, duration: '1m' },  // Ramp to 500 TPS over one minute.
        { target: 1000, duration: '2m' }, // Continue ramping to 1,000 TPS.
        { target: 1000, duration: '5m' }, // Hold 1,000 TPS for five minutes.
        { target: 0, duration: '30s' },   // Ramp down to zero.
      ],
      exec: 'readBlog',
    },
  },

  // Acceptance thresholds: p95/p99 latency and error rate.
  thresholds: {
    http_req_failed: ['rate<0.01'],             // Error rate below 1%.
    'http_req_duration{scenario:read_blog_scenario}':   ['p(95)<50',  'p(99)<100'],
  },
};
// Call the blog list API.
export function readBlog() {
  const res = http.get(`${BASE_URL}/blogs?page=1&limit=20`);

  check(res, {
    'list returns status 200': (r) => r.status === 200,
  });
}
