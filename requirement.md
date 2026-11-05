# BACKEND TECHNICAL ASSIGNMENT

## Blog Content Service

**Golang / REST API / Architecture / Concurrency / Performance**

---

# 1. Objective

Build a **Blog Content Service** in **Golang**.

This assignment evaluates the candidate's understanding of:

- RESTful API design
- Clean Architecture / Hexagonal Architecture
- Database and transaction design
- Role-Based Access Control (RBAC)
- Concurrency and data consistency
- Performance optimization
- Unit tests and integration tests

---

# 2. Functional requirements

## 2.1 User & Authorization

Implement **mock authentication** with the following roles:

| Role | Permissions |
|---|---|
| Admin | Full access, including soft delete and report export |
| Editor | Create and update blogs |
| Reader | View the blog list and blog details |
| Reporter | View blogs and export reports |
| Anonymous | View the blog list and blog details |

Authentication can be mocked using:

- Predefined users.
- A request header.

**A real login or registration system is not required.**

---

# 2.2 Blog management

Each blog must have the following fields:

| Field | Data type | Description |
|---|---|---|
| `id` | UUID / BIGINT | Unique ID |
| `title` | String | Blog title |
| `author` | String | Author name |
| `content` | Text | Full content |
| `description` | String | Short description |
| `created_at` | Timestamp | Creation time |
| `updated_at` | Timestamp | Time of the most recent update |
| `deleted_at` | Nullable timestamp | Soft delete time |
| `created_by` | User ID | Creator |
| `updated_by` | User ID | Most recent editor |
| `deleted_by` | Nullable User ID | User who deleted the blog |
| `views` | Integer | Total unique daily views |

## Required features

### Blog list

Return a list of blogs with:

- Pagination.
- A response that only needs to include:
  - `title`
  - `description`

### Blog detail

Return the full content of a blog.

### Create blog

Only the following roles may create blogs:

- `Editor`
- `Admin`

### Update blog

Only the following roles may update blogs:

- `Editor`
- `Admin`

### Delete blog

Use **soft delete**.

Only:

- `Admin`

may delete blogs.

### Report export

Allow exporting a blog report in the following format:

```text
CSV
```

Allowed roles:

- `Admin`
- `Reporter`

### Rules for soft-deleted blogs

A soft-deleted blog:

- Must not appear in public APIs.
- Must not appear in regular reports.

---

# 2.3 Blog view counter

Implement counting of:

> Unique Daily Views

That is, a view is unique per:

```text
Blog + IP + day
```

Rules:

### Rule 1

Each unique IP address that reads a specific blog is counted as:

```text
1 view / day
```

Example:

```text
IP: 1.1.1.1
Blog: blog-001
Date: 2026-11-05
```

First request:

```text
views + 1
```

---

### Rule 2

If the same IP sends multiple requests to the same blog on the same day:

```text
The view count must not increase again.
```

Example:

```text
1.1.1.1 -> blog-001 -> request 1
1.1.1.1 -> blog-001 -> request 2
1.1.1.1 -> blog-001 -> request 3
```

Within the same day, only this is counted:

```text
1 view
```

---

### Rule 3

If the same IP reads the same blog on the next day:

```text
views + 1
```

Example:

```text
2026-11-05: 1.1.1.1 -> blog-001 = +1
2026-11-06: 1.1.1.1 -> blog-001 = +1
```

---

### Rule 4

Different IPs must be counted independently.

Example:

```text
1.1.1.1 -> blog-001 = +1
2.2.2.2 -> blog-001 = +1
3.3.3.3 -> blog-001 = +1
```

Result:

```text
views = 3
```

---

### Rule 5

**Concurrent requests** must not cause double counting.

For example, with 100 concurrent requests:

```text
same IP
same blog
same day
```

the result must still be only:

```text
+1 view
```

Not:

```text
+100 views
```

---

### Rule 6

The blog list API:

```text
GET /api/v1/blogs
```

**must not increase views.**

Only reading a blog's detail affects the view counter.

---

### Rule 7

Use:

```text
UTC
```

as the reference for determining the day.

---

### Rule 8

The service must correctly identify the client IP when the application runs behind a:

```text
trusted reverse proxy
```

Example:

```text
Client
   ↓
Nginx / Load Balancer
   ↓
Golang API
```

The application must handle forwarded-IP headers correctly, but must only trust proxies that are configured as **trusted proxies**.

---

# 2.4 API requirements

Implement the following endpoints:

| Method | Endpoint | Function |
|---|---|---|
| GET | `/api/v1/blogs` | List blogs |
| GET | `/api/v1/blogs/{id}` | Get blog detail |
| POST | `/api/v1/blogs` | Create blog |
| PUT | `/api/v1/blogs/{id}` | Update blog |
| DELETE | `/api/v1/blogs/{id}` | Soft delete blog |
| GET | `/api/v1/reports/blogs` | Export CSV report |

Every API must fully implement:

- Appropriate input validation.
- Appropriate HTTP status codes.
- A consistent error response format.

---

# 3. Non-functional requirements

## 3.1 Architecture

The application must:

- Use Golang.
- Apply either:
  - Clean Architecture, or
  - Hexagonal Architecture.
- Separate:
  - Business logic.
  - HTTP handlers.
  - Persistence/database logic.
- Use interfaces where appropriate to isolate infrastructure dependencies.
- Support Dependency Injection.
- Use context propagation.
- Support graceful shutdown.
- Use structured logging.

A possible structure:

```text
HTTP Handler
      ↓
Use Case / Application
      ↓
Domain
      ↓
Repository Interface
      ↓
Infrastructure / PostgreSQL
```

---

# 3.2 Data consistency

The system must guarantee data consistency.

## No duplicate daily views

Under many concurrent requests, the system must guarantee:

```text
same blog
+ same IP
+ same day
= only 1 view
```

---

## No race conditions

Especially when incrementing the counter:

```text
views = views + 1
```

must be handled safely under concurrency.

---

## Soft-deleted blogs must not be accessible through public APIs

For example, if:

```text
deleted_at IS NOT NULL
```

then:

```text
GET /api/v1/blogs/{id}
```

must not return that blog through the public API.

---

## Audit fields must be accurate

Fields such as:

```text
created_by
updated_by
deleted_by

created_at
updated_at
deleted_at
```

must be updated accurately.

---

## Transactions

The candidate must clearly explain:

- Where a transaction begins.
- Where a transaction ends.
- Which operations are in the same transaction.
- Which parts use eventual consistency.

If eventual consistency is used, explain the:

```text
trade-off
```

of that design.

---

# 3.3 Performance

The system has the following throughput targets:

| Operation | Target |
|---|---:|
| Create blog | 1,000 TPS |
| Update blog | 1,000 TPS |
| Read blog | 10,000 TPS |
| List blogs | 10,000 TPS |

Where:

```text
TPS = Transactions Per Second
```

---

# Performance evaluation

The performance requirements must be verified with a:

```text
reproducible load test
```

that is, a load test that can be rerun and produces comparable results.

The test report must clearly describe:

- Test environment.
- Concurrency level.
- Dataset size.
- p95 latency.
- p99 latency.
- Error rate.
- Actual throughput.

An example report might include:

```text
Environment:
CPU: 8 cores
RAM: 16 GB
PostgreSQL: 16
Go: x.x

Dataset:
1,000,000 blogs

Concurrency:
500 clients

Results:
Throughput: 8,500 req/s
p95: 25 ms
p99: 50 ms
Error rate: 0.01%
```

---

# If the performance target is not met

If the system cannot reach:

```text
Create: 1,000 TPS
Update: 1,000 TPS
Read/List: 10,000 TPS
```

it is not necessarily considered a failure.

You must:

1. Identify the bottleneck.
2. Explain the cause.
3. Propose an optimization.
4. Provide benchmark/test results that support your conclusions.

Possible bottlenecks include:

```text
Database connection pool
Database lock
Index
Disk I/O
Network
Serialization
View counting
Cache
```

---

# 4. Testing requirements

Automated tests must cover the following cases.

## Role-based permission checks

For example:

```text
Admin
Editor
Reader
Reporter
Anonymous
```

must be tested for API access.

---

## Blog creation and update

Test:

- Successful blog creation.
- Blog creation with invalid data.
- Successful blog update.
- Updating a blog that does not exist.
- Insufficient permissions.

---

## Soft delete

Test:

```text
Admin -> delete -> success
Editor -> delete -> forbidden
```

After deletion:

```text
GET blog
```

must not return that blog from the public API.

---

## Pagination

Test:

- Page.
- Limit.
- Boundary.
- Empty result.
- Invalid pagination input.

---

## Daily unique view counting

Verify the logic:

```text
blog + IP + UTC date
```

---

## Concurrent requests from the same IP

Example:

```text
100 concurrent requests
same IP
same blog
same day
```

Expected:

```text
views + 1
```

---

## Concurrent requests from different IPs

Example:

```text
100 concurrent requests
100 different IPs
same blog
```

Expected:

```text
views + 100
```

---

## Error handling and invalid input

For example:

```text
invalid UUID
missing title
invalid pagination
blog not found
permission denied
```

---

## CSV export

Must test:

- Permissions.
- Output content.
- CSV format.
- Soft-deleted blogs do not appear.

---

## Integration test

At least:

```text
1 integration test
```

must use a:

```text
real database
```

The database must not be mocked in every test.

---

# 5. Deliverables

You must submit the following.

## 1. Source code

The complete source code of the:

```text
Golang Blog Content Service
```

---

## 2. README.md

The README must include instructions for:

- Setup.
- Installation.
- Running the application.
- Using the API.

---

## 3. Architecture documentation

Architecture documentation explaining:

- Project structure.
- Layers.
- Responsibilities of each layer.

Example:

```text
Domain
Application
Infrastructure
Delivery / HTTP
```

---

## 4. Database schema

Including:

- Migrations.
- Relationships.
- Indexes.
- Constraints.

---

## 5. API documentation

You may use:

- Request/response examples.

or:

- An OpenAPI specification.

Example:

```text
Swagger / OpenAPI
```

---

## 6. Tests

Including:

- Unit tests.
- Integration tests.

---

## 7. Performance report

The performance report must include:

- Benchmark results.
- Bottlenecks.
- Optimization recommendations.

---

## 8. Technical explanation

Explain the important parts of the solution, such as:

- Main functions.
- Business flow.
- Key design decisions.
- Trade-offs.

---

## Docker Compose

The entire solution must be runnable locally with:

```text
Docker Compose
```

For example:

```text
docker compose up
```

should start all required components.

---

# 6. Evaluation criteria

| Criterion | Weight |
|---|---:|
| Functional correctness | 25% |
| Architecture and code quality | 20% |
| Data consistency and concurrency | 20% |
| Testing | 15% |
| Performance and optimization | 10% |
| Documentation and explanation | 10% |

Total:

```text
100%
```

---

# Important note

You must be able to **explain your design decisions and implementation**.

Code written with AI assistance is:

```text
accepted
```

However, you remain responsible for:

- Understanding the code.
- Validating the code.
- Testing the solution.
- Explaining the solution.

In other words, using AI is not a problem, but during the interview or code review you must understand why the code is designed the way it is.

---

# 7. Bonus challenges

These are advanced items. They are not part of the core requirements but can help your solution score higher.

## Bonus 1 – Caching

Implement caching without breaking the logic of:

```text
unique daily view
```

For example, if blog detail is cached, reads of the blog must still be recorded as views correctly.

---

## Bonus 2 – Database indexing

Optimize the queries for:

```text
List blog
Blog detail
```

using database indexes.

Choose appropriate indexes.

For example, they might involve:

```text
deleted_at
created_at
id
```

---

## Bonus 3 – Concurrent view updates

Handle a large volume of concurrent view updates efficiently without overloading the database.

Example of the problem to solve:

```text
10000 request/s
       ↓
View counter
       ↓
Database
```

Each request should not create an unnecessary database bottleneck.

---

## Bonus 4 – Safe recovery

The system must recover safely when a dependency used for view counting fails.

For example, if the solution uses:

```text
Redis
```

to support view counting, explain:

```text
What happens if Redis goes down?
```

and how the system handles it so that it:

- Does not lose data.
- Does not create too many duplicates.
- Does not bring down the main API with it.

---

# Bonus 5 – Compare at least two view-counting approaches

Compare at least:

```text
2 approaches
```

for counting unique daily views and explain the trade-offs.

For example, you might compare:

```text
Approach A:
PostgreSQL unique constraint + transaction

Approach B:
Redis SET/SADD + asynchronous DB aggregation
```

Explain the pros and cons of each approach in terms of:

- Consistency.
- Performance.
- Complexity.
- Scalability.
- Failure recovery.
- Database load.
