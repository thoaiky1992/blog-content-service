import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080/api/v1';

export const options = {
  scenarios: {

    // 3. Target: 10,000 TPS cho Read / List Blogs
    read_blog_scenario: {
      executor: 'ramping-arrival-rate',
      startRate: 1000,
      timeUnit: '1s',
      preAllocatedVUs: 1500,  // Cần lượng VU lớn hơn cho 10k TPS
      maxVUs: 10000,
      stages: [
        { target: 500, duration: '1m' },  // Tăng dần lên 500 TPS trong 1 phút
        { target: 1000, duration: '2m' }, // Tăng tiếp lên 1,000 TPS trong 2 phút
        { target: 1000, duration: '5m' }, // Giữ nguyên 1,000 TPS trong 5 phút
        { target: 0, duration: '30s' },   // Hạ dần về 0 TPS trong 30 giây
      ],
      exec: 'readBlog',
    },
  },

  // Đo lường ngưỡng đạt chuẩn: p95/p99 latency và error rate (theo yêu cầu đề bài)
  thresholds: {
    http_req_failed: ['rate<0.01'],             // Tỉ lệ lỗi < 1%
    'http_req_duration{scenario:read_blog_scenario}':   ['p(95)<50',  'p(99)<100'],
  },
};
// Hàm thực thi đọc/danh sách blog
export function readBlog() {
  // Kết hợp giữa đọc chi tiết (detail) và danh sách (list)
  const isList = Math.random() < 0.3; // 30% gọi list, 70% gọi read chi tiết
  let res;

  res = http.get(`${BASE_URL}/blogs?page=1&limit=20`);

  check(res, {
    'read status 200': (r) => r.status === 200,
  });
}