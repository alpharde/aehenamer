# agents.md

## Code Style

- Follow standard Go conventions: `gofmt`, `goimports`, and `golangci-lint`.
- Keep packages small and focused. Name them short, lowercase, no underscores.
- Avoid unnecessary abstractions — prefer simple, readable code over clever code.
- Define interfaces at the point of use, not the point of implementation. Keep them small.

## Error Handling

- Always handle errors explicitly — never ignore them with `_`.
- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`.
- Use `errors.Is` / `errors.As` for matching, not string comparison.
- Reserve `panic` for unrecoverable programmer mistakes, not runtime errors.

## Concurrency

- Pass `context.Context` as the first argument to any blocking or I/O function.
- Never store a `Context` in a struct. Never use `context.Background()` deep in business logic.
- Always manage goroutine lifecycles — use `errgroup`, `sync.WaitGroup`, or channels.
- Protect shared state with mutexes or by confining it to a single goroutine.

## Testing

- Write table-driven tests using `t.Run` for clarity and coverage.
- Use the standard `testing` package. Reach for `testify` only when it genuinely reduces noise.
- Prefer real implementations over mocks where feasible (e.g., `httptest`, in-memory stores).
- Keep tests close to the code they test; use `_test` packages for black-box testing.
- Benchmark with `testing.B` before optimizing anything.

## Dependencies & Modules

- Keep `go.mod` tidy — run `go mod tidy` before committing.
- Minimize external dependencies; prefer the standard library.
- Pin versions explicitly and review dependency updates carefully.

## General

- Measure before optimizing. Use `pprof` for profiling.
- Log at boundaries (entry/exit of services), not inside every function.
- Prefer explicit over implicit — avoid `init()` and global state.

## API Design

- Propagate `context.Context` through every layer of an API — from handler to store.
- Never drop context mid-call or substitute it with `context.Background()` silently.
- Honor context cancellation and deadlines in all I/O and long-running operations.
- Design APIs so callers can always pass a context; if a function does I/O, it takes a `ctx`.

## Documentation

- Document every exported type, function, method, and constant — no exceptions.
- Follow Go doc conventions: start the comment with the name of the thing being documented.
- Package-level comments should explain purpose and usage, not implementation details.
- Include examples (`Example*` functions) for non-trivial exported APIs.

```go
// UserStore retrieves and persists user records.
type UserStore interface { ... }

// GetByID returns the user with the given ID, or ErrNotFound if absent.
func (s *store) GetByID(ctx context.Context, id string) (*User, error) { ... }
```

## Testing (expanded)

- Write tests as you write code — not after, not when asked. Tests are not optional.
- Every exported function and critical internal path must have at least one test.
- Cover edge cases, error paths, and boundary conditions — not just the happy path.
- Use `t.Helper()` in shared test utilities to keep failure output pointing at the call site.
- Integration tests live in `test/` or behind a build tag (e.g., `//go:build integration`).

## Microservice Readiness

- Expose `/healthz` (liveness) and `/readyz` (readiness) endpoints on a dedicated port.
- Liveness indicates the process is alive; readiness indicates it can serve traffic.
- Handle `SIGTERM` and `SIGINT` with graceful shutdown: stop accepting new requests, finish in-flight work, release resources, then exit.
- Set explicit timeouts on `http.Server`: `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- Use `errgroup` with a context to coordinate shutdown across goroutines cleanly.

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()
```

## Security

- Never log secrets, tokens, passwords, or PII — scrub them before logging.
- Validate and sanitize all external input; never trust data from outside the process boundary.
- Use `crypto/rand` for all randomness that touches security; never `math/rand`.
- Enforce TLS for all external communication; keep cipher suites and TLS version current.
- Follow the principle of least privilege for service accounts, DB roles, and IAM policies.
- Scan dependencies for known CVEs regularly (e.g., `govulncheck`).
- Set strict timeouts on all outbound HTTP clients — never use the default zero-timeout client.
