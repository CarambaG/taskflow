# TaskFlow

Production-like REST API for team task management. The service implements JWT authentication, role-based access control, change history, comments, optimistic locking, Redis list caching and a single-query MySQL analytics report.

## Stack

- Go 1.23, `net/http`, `chi`
- MySQL 8.4 with InnoDB transactions
- Redis 7.4
- JWT HS256 and bcrypt
- OpenAPI 3.0 with embedded Swagger UI
- Docker, Docker Compose and GitHub Actions
- `desertbit/closer` for coordinated graceful shutdown
- structured JSON logging with `log/slog`

## Quick start

Requirements: Docker Engine with Docker Compose v2.

```bash
cp .env.example .env
```

Replace `JWT_SECRET`, `MYSQL_PASSWORD`, `MYSQL_ROOT_PASSWORD` and `REDIS_PASSWORD` in `.env` with strong random hexadecimal values. Compose builds `MYSQL_DSN` from the database variables; keep the example DSN consistent only when running the binary outside Compose. Then run:

```bash
docker compose up --build -d
docker compose ps
curl http://localhost:8080/readyz
```

The first MySQL container startup applies `migrations/000001_init.up.sql`. Swagger UI is available at [http://localhost:8080/swagger/](http://localhost:8080/swagger/); its static assets are embedded in the binary and do not require a CDN. The raw contract is at [http://localhost:8080/openapi.yaml](http://localhost:8080/openapi.yaml).

Stop the environment without deleting data:

```bash
docker compose down
```

## Configuration

All configuration is supplied through environment variables; secrets are not hardcoded.

| Variable | Default | Purpose |
| --- | --- | --- |
| `APP_ENV` | `development` | Runtime environment label |
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `HTTP_READ_TIMEOUT` | `10s` | Maximum request read time |
| `HTTP_WRITE_TIMEOUT` | `15s` | Maximum response write time |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful HTTP shutdown deadline |
| `MYSQL_DATABASE` | `taskflow` | Database created by the MySQL container |
| `MYSQL_USER` | `taskflow` | Application database user |
| `MYSQL_PASSWORD` | required | Application database password |
| `MYSQL_ROOT_PASSWORD` | required | MySQL administrative password used only by the container |
| `MYSQL_DSN` | required outside Compose | MySQL DSN with `parseTime=true`; Compose builds it from the variables above |
| `REDIS_ADDR` | `localhost:6379` | Redis endpoint |
| `REDIS_PASSWORD` | required by Compose | Redis password used by both the server and application |
| `REDIS_DB` | `0` | Redis logical database |
| `JWT_SECRET` | required | HS256 secret, minimum 32 characters |
| `JWT_TTL` | `24h` | Access token lifetime |
| `TASK_CACHE_TTL` | `5m` | Task-list cache lifetime |
| `RATE_LIMIT_RPS` | `20` | Per-IP token refill rate |
| `RATE_LIMIT_BURST` | `40` | Per-IP burst size |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

## API overview

| Method and path | Access | Success status |
| --- | --- | --- |
| `POST /api/v1/register` | Public | `201` |
| `POST /api/v1/login` | Public | `200` |
| `POST /api/v1/teams` | Authenticated | `201` |
| `GET /api/v1/teams` | Authenticated | `200` |
| `POST /api/v1/teams/{id}/invite` | Owner or admin | `201` |
| `PATCH /api/v1/teams/{id}/members/{user_id}/role` | Owner | `200` |
| `POST /api/v1/tasks` | Team member | `201` |
| `GET /api/v1/tasks?team_id=...` | Team member | `200` |
| `PUT /api/v1/tasks/{id}` | Depends on task role | `200` |
| `GET /api/v1/tasks/{id}/history` | Team member | `200` |
| `POST /api/v1/tasks/{id}/comments` | Team member | `201` |
| `GET /api/v1/tasks/{id}/comments` | Team member | `200` |
| `GET /api/v1/teams/{team_id}/stats` | Owner or admin | `200` |

Send the token as `Authorization: Bearer <token>`. Request bodies are limited to 1 MiB, unknown JSON fields are rejected, and list pagination is limited to 100 records.

### Role matrix

| Operation | Owner | Admin | Member |
| --- | ---: | ---: | ---: |
| Invite admin/member | Yes | Yes | No |
| Change admin/member roles | Yes | No | No |
| Read team tasks, history and comments | Yes | Yes | Yes |
| Create a task | Yes | Yes | Yes |
| Edit any team task | Yes | Yes | No |
| Edit a task they created | Yes | Yes | Yes |
| Change status of an assigned task | Yes | Yes | Yes |
| Reassign a task assigned to them but created by someone else | Yes | Yes | No |
| View team analytics | Yes | Yes | No |

Resources outside the current user's teams are concealed with `404`; this prevents cross-team existence leaks. Authorization is enforced in the service layer rather than only in HTTP middleware.

Only the owner can change an existing member's role. The target role can be `admin` or `member`; the owner's own role is immutable through the API.

## Example flow

Register and log in:

```bash
curl -sS -X POST http://localhost:8080/api/v1/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","password":"strong-password","name":"Owner"}'

curl -sS -X POST http://localhost:8080/api/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","password":"strong-password"}'
```

Create a team and a task:

```bash
TOKEN='<token from login>'

curl -sS -X POST http://localhost:8080/api/v1/teams \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Platform"}'

curl -sS -X POST http://localhost:8080/api/v1/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"team_id":1,"title":"Add readiness probe","description":"Expose dependency checks","status":"todo"}'
```

Change an existing member's role as the team owner:

```bash
curl -sS -X PATCH http://localhost:8080/api/v1/teams/1/members/2/role \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"role":"admin"}'
```

Update a task using the current version returned by create/list:

```bash
curl -sS -X PUT http://localhost:8080/api/v1/tasks/1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"version":1,"status":"done"}'
```

If another request has already changed version `1`, the response is `409 Conflict`. To clear an assignee, send `"assignee_id": null`; omitting the field leaves it unchanged.

## Errors and HTTP statuses

Errors use one predictable envelope:

```json
{
  "error": {
    "code": "conflict",
    "message": "resource already exists or version is stale",
    "request_id": "1723456789-42"
  }
}
```

- `400` - malformed JSON, invalid filters or validation failure
- `401` - missing/invalid JWT or invalid credentials
- `403` - authenticated user lacks permission
- `404` - missing resource or resource hidden by team isolation
- `409` - duplicate email/membership or stale task version
- `429` - per-IP rate limit exceeded
- `500` - unexpected internal error; details appear only in logs
- `503` - readiness dependency unavailable

## Consistency and concurrency

- Team creation and owner membership insertion share one transaction.
- Task creation and its initial history record share one transaction.
- Task update locks the current row with `SELECT ... FOR UPDATE`, checks the client-supplied version, writes the update with `WHERE version = ?`, increments the version and inserts history before commit.
- The history JSON stores old and new values for every changed field.
- Assignees are checked against `team_members`; cross-team assignment is rejected.
- `closed_at` is set when status becomes `done` and cleared when a task is reopened.

## Cache design

`GET /api/v1/tasks` is cached for five minutes. The SHA-256 cache key includes team ID, status, assignee, limit and offset, and is namespaced by a per-team generation number. Creating or updating a task atomically increments that generation. A request that started before the update may still finish writing its old generation, but subsequent reads use the new generation and can never observe that stale value. Old keys expire automatically by TTL. Redis errors are logged at `WARN` and reads safely fall back to MySQL.

## SQL analytics

`GET /api/v1/teams/{team_id}/stats` uses one MySQL 8 query with CTEs, joins, grouping, aggregates and date arithmetic. It returns:

- counts by task status;
- the top three assignees by tasks closed in the last 30 days;
- average close time in seconds;
- total task-comment count.

The query is always filtered by one `team_id`, and the service checks owner/admin membership before executing it. There is no N+1 query pattern.

## Graceful shutdown

`closer.CloseOnInterrupt` handles `SIGINT` and `SIGTERM`. The HTTP server enters shutdown during the closer's `OnClosing` phase, stops accepting connections and waits up to `SHUTDOWN_TIMEOUT` for active handlers. MySQL and Redis close afterward in `OnClose`. This order avoids terminating dependencies while requests are still running and avoids a wait-group deadlock.

## Migrations

The schema has reversible migrations:

```text
migrations/000001_init.up.sql
migrations/000001_init.down.sql
```

For a running development Compose stack:

```bash
make migrate-up
make migrate-down
```

The down migration deletes all application tables and data; use it only against a disposable database. In deployment pipelines, run the same files with a dedicated migration job before starting the new application version.

## Development and tests

```bash
make build
make test
make lint
```

The SQL report integration test requires MySQL with the schema creation privilege:

```bash
TEST_DATABASE_DSN='taskflow:taskflow@tcp(127.0.0.1:3306)/taskflow_test?parseTime=true&charset=utf8mb4' \
  make test-integration
```

`go test -race ./...`, `go vet ./...` and the MySQL integration test run in GitHub Actions. The integration test creates isolated records, verifies all report metrics and removes its data afterward.

## Project layout

```text
cmd/api/                         application entry point
api/                             embedded OpenAPI contract
internal/app/                    dependency wiring and lifecycle
internal/auth/                   JWT issuing and validation
internal/cache/                  Redis task-list cache
internal/config/                 environment configuration
internal/domain/                 entities, roles and domain errors
internal/repository/             persistence interfaces
internal/repository/mysql/       transactional MySQL implementation
internal/service/                business rules and authorization
internal/transport/httpapi/      handlers, router and middleware
migrations/                      reversible MySQL schema
tests/integration/               SQL report integration test
```

## Commit history

The repository is intentionally developed through focused conventional commits. Inspect it with:

```bash
git log --oneline --reverse
```
