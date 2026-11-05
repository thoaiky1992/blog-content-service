import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080/api/v1';
const EDITOR_ID = __ENV.EDITOR_ID;

if (!EDITOR_ID) {
  throw new Error('EDITOR_ID is required and must belong to a user with blog:create permission');
}

export const options = {
  scenarios: {
    // Target: 1,000 TPS for the create-blog API.
    create_blog_scenario: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 500,
      maxVUs: 2000,
      stages: [
        { target: 300, duration: '30s' },
        { target: 700, duration: '1m' },
        { target: 1000, duration: '3m' },
        { target: 0, duration: '30s' },
      ],
      exec: 'createBlog',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{scenario:create_blog_scenario}': [
      'p(95)<50',
      'p(99)<100',
    ],
  },
};

function blogPayload() {
  return JSON.stringify({
    title: `Load test blog ${__VU}-${__ITER}`,
    author: 'k6 load tester',
    content: 'Lorem ipsum dolor sit amet, consectetur adipiscing elit. '.repeat(20),
    description: 'Created by the k6 load test',
  });
}

export function createBlog() {
  const res = http.post(`${BASE_URL}/blogs`, blogPayload(), {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': EDITOR_ID,
    },
  });

  check(res, {
    'create returns status 201': (r) => r.status === 201,
    'create includes location header': (r) => Boolean(r.headers.Location),
  });
}
