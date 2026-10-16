# Shared HTTP Runtime

The User, Account and Transaction entry points use `src/httpapi`. Business routes
are still pending; the current handlers return JSON `404 NOT_FOUND`. The worker
remains a separate scaffold. Health endpoints are T50, not implemented here.

## Configuration

All three servers require `HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE`; they
never start a plaintext listener and do not trust forwarded-protocol headers.
Use separate addresses when running multiple services on one machine.

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `HTTP_ADDR` | `:8443` | Listen address; numeric port, including `0` for tests. |
| `HTTP_TLS_CERT_FILE` | Required | PEM certificate path. |
| `HTTP_TLS_KEY_FILE` | Required | PEM private-key path; keep outside source control. |
| `HTTP_MAX_BODY_BYTES` | `65536` | Maximum bytes read from a request body. |
| `HTTP_MAX_HEADER_BYTES` | `16384` | Go server header-size setting. |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Header read timeout. |
| `HTTP_READ_TIMEOUT` | `10s` | Entire request read timeout. |
| `HTTP_REQUEST_TIMEOUT` | `10s` | Handler processing deadline. |
| `HTTP_WRITE_TIMEOUT` | `15s` | Response write timeout; must exceed processing deadline. |
| `HTTP_IDLE_TIMEOUT` | `1m` | Idle keep-alive timeout. |
| `HTTP_SHUTDOWN_TIMEOUT` | `10s` | Drain interval before forced close. |

Limits and durations must be positive. Configuration/startup errors do not print
environment values or certificate paths. Defaults are initial operational bounds,
not performance targets. Network-level malformed requests, header limits, and TLS
failures are handled by Go's HTTP server and need not have JSON response bodies.

## Handler Contract

Use `httpapi.DecodeJSON` inside the runtime for one uncompressed JSON object,
with `application/json` content type. It rejects malformed input, trailing JSON,
unknown fields, and oversized bodies (including chunked requests). Endpoint
handlers must still validate required fields and business semantics.

Use `WriteJSON` / `WriteError` with fixed, reviewed error messages; never pass
raw driver errors, passwords, hashes, tokens, or submitted values into errors.
Responses use JSON, `Cache-Control: no-store`, and `nosniff`. Each request receives
a server-generated `X-Request-ID`; client-supplied IDs are not trusted.

Generic runtime errors are `400 INVALID_REQUEST`, `413 REQUEST_TOO_LARGE`,
`415 UNSUPPORTED_MEDIA_TYPE`, `500 INTERNAL_ERROR`, and
`503 REQUEST_TIMEOUT` (deadline) or `503 REQUEST_CANCELLED` (client cancellation). Domain/authentication codes are later endpoint tasks.
Handlers must propagate `r.Context()` to database and remote calls. The timeout
wrapper cancels that context and bounds response waiting, but cannot forcibly
stop code that ignores cancellation. Streaming and WebSockets are outside this
bounded JSON runtime.

## Logging and Shutdown

Access logs allow only generated request ID, known HTTP method (unknown methods
become `OTHER`), status and duration, plus standard JSON log timestamp/level.
No headers, URL/path/query, body, response payload, raw exception or panic value
is logged. Server diagnostics that can contain client input are suppressed;
fixed lifecycle messages record startup, stopping and shutdown timeout. Panic
recovery discards buffered handler headers/status/body and returns one safe 500
JSON response; partially prepared success responses are never committed.

SIGINT/SIGTERM stop accepting requests and drain in-flight work. After the drain
timeout, the runtime cancels active contexts, closes connections and returns a
failure. Completed queued operations remain independent of these HTTP processes.
TLS setup failures close the listener. Minimum supported TLS version is 1.2.

## Validation

`go test -race ./src/httpapi` runs configuration, handler, redaction, deadline,
TLS and shutdown tests using temporary certificates and local ephemeral ports.
No database, broker, account endpoints or persistent certificate files are needed.
Run `make check` for repository-wide formatting, vet, tests and builds.
