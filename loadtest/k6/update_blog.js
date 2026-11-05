import http from 'k6/http';
import crypto from 'k6/crypto';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080/api/v1';
const EDITOR_ID = __ENV.EDITOR_ID;
const ROWS = Number(__ENV.ROWS || 1000000);
const BLOG_ID = __ENV.BLOG_ID;

if (!EDITOR_ID) {
  throw new Error('EDITOR_ID is required and must belong to a user with blog:update permission');
}

export const options = {
  scenarios: {
    // Target: 1,000 TPS for the update-blog API.
    update_blog_scenario: {
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
      exec: 'updateBlog',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{scenario:update_blog_scenario}': [
      'p(95)<50',
      'p(99)<100',
    ],
  },
};

// Blog i has ID md5('blog-' + i)::uuid, matching seed_blogs.sql.
function blogIdByIndex(index) {
  const hash = crypto.md5(`blog-${index}`, 'hex');
  return `${hash.slice(0, 8)}-${hash.slice(8, 12)}-${hash.slice(12, 16)}-${hash.slice(16, 20)}-${hash.slice(20)}`;
}

function blogPayload() {
  return JSON.stringify({
    title: `Updated by k6 ${__VU}-${__ITER}`,
    author: 'k6 load tester',
    content: 'Content updated by the k6 load test. '.repeat(20),
    description: 'Updated by the k6 load test',
  });
}

export function updateBlog() {
  // BLOG_ID is useful for contention tests; the default spreads traffic across seeded data.
  const blogId = BLOG_ID || blogIdByIndex(1 + Math.floor(Math.random() * ROWS));
  const res = http.put(`${BASE_URL}/blogs/${blogId}`, blogPayload(), {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': EDITOR_ID,
    },
    // Group dynamic URLs into one time series to avoid per-blog cardinality growth.
    tags: { name: 'PUT /api/v1/blogs/:id' },
  });

  check(res, {
    'update returns status 200': (r) => r.status === 200,
  });
}
