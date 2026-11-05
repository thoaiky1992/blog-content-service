import http from 'k6/http';
import { check } from 'k6';
import k6crypto from 'k6/crypto';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080/api/v1';
const DETAIL_POOL = Number(__ENV.DETAIL_POOL || 1000);
const BLOG_ID = __ENV.BLOG_ID || '';

// Pre-compute toàn bộ pool ID ngay khi init script
const BLOG_IDS = Array.from({ length: DETAIL_POOL }, (_, i) => {
  const hash = k6crypto.md5(`blog-${i + 1}`, 'hex');
  return `${hash.slice(0, 8)}-${hash.slice(8, 12)}-${hash.slice(12, 16)}-${hash.slice(16, 20)}-${hash.slice(20)}`;
});

export const options = {
  scenarios: {
    read_detail_blog_scenario: {
      executor: 'ramping-arrival-rate',
      startRate: 1000,
      timeUnit: '1s',
      preAllocatedVUs: 500,
      maxVUs: 5000,
      stages: [
        { target: 10000, duration: '10s' }, // Ramp up nhanh lên 10k
        { target: 10000, duration: '3m' }, // Giữ mức 10k ổn định trong 60s
        { target: 0, duration: '5s' },
      ],
      exec: 'readDetailBlog',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{scenario:read_detail_blog_scenario}': ['p(95)<50', 'p(99)<100'],
  },
  discardResponseBodies: true,
};

export function readDetailBlog() {
  const blogId = BLOG_ID || BLOG_IDS[Math.floor(Math.random() * DETAIL_POOL)];

  const res = http.get(`${BASE_URL}/blogs/${blogId}`, {
    tags: { name: 'GET /api/v1/blogs/:id' },
  });

  check(res, {
    'read detail status 200': (r) => r.status === 200,
  });
}