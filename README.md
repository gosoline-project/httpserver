# httpserver

A lightweight, opinionated HTTP server helper library built on top of [gin](https://github.com/gin-gonic/gin) and the [gosoline](https://github.com/justtrackio/gosoline) ecosystem. It provides structured request binding, consistent response types, middleware (logging, recovery, compression, CORS), and an application-friendly router definition API for building modular services.

## Features

- Declarative routing with grouping and middleware chaining.
- Generic request binding: automatically bind JSON, form, query, headers, URI params, protobuf, XML, etc. using struct tags.
- Response abstractions: plain, status, JSON responses with fluent options (headers, status code).
- Middleware: logging, error handling, recovery, metrics, profiling, compression, CORS.
- Simple test helpers and suite integration.
- Composable router factories for modular service assembly.

See the [HTTP Server reference](https://gosoline-project.github.io/docs/reference/package-httpserver/) and [metric schema reference](https://gosoline-project.github.io/docs/reference/metric-schema/) for metric details.

## Installation

```bash
go get github.com/gosoline-project/httpserver
```

### Go version and dependency security

Go **1.26.11 or newer** is required. Consumers on the Go 1.27 branch must use
**1.27.4 or newer**: the `go 1.26.11` minimum does not upgrade an
already-newer toolchain to its patched release. This raises the former Go
1.25.7 minimum; downstream applications using Go 1.25 must upgrade their
toolchain. CI reads the minimum from `go.mod`, and `mise.toml` pins the same
patched release.

The dependency security refresh retains Gin 1.11.0 and JWT v5.3.0. The
current graph selects Gosoline v0.69.4-0.20261119192118-ea1ab1cd583b,
aligned with OpenTelemetry 1.47 and including its metric contract.
The refresh updates the affected QUIC, OAuth, HTTP/2, gRPC,
OpenTelemetry, AWS event-stream, compression, and Docker test-harness
dependencies to their first fixed releases, including any newer versions
required by that combined module graph.

The current graph uses Brotli 1.2.7, Compress 1.20.4, x/net 0.62.0,
OpenTelemetry API, SDK, log SDK, and both OTLP metric exporters 1.47.0,
and the OpenTelemetry HTTP/gRPC log exporters 0.23.0. The aligned metric
exporters support map-valued attributes. Compression and telemetry updates may
change malformed-input handling and exporter behavior; downstream integrations
should validate their own traffic and collector configuration.

The selected Gosoline revision also changes its guard interface, removes
CrateDB support, changes Avro codec and S3 checksum behavior, and updates
telemetry and compression. Its default Avro codec still has no map-allocation
limit. Review [Gosoline's compatibility notes](https://github.com/justtrackio/gosoline/blob/ea1ab1cd583bd646cc4a4c45e9e7610695798653/README.md#requirements-and-upgrade-compatibility)
before upgrading downstream integrations.

Consumers should also review these changes in the selected dependency graph:

- The Go patch releases fix a server crash; malformed HTTP/2, MIME, and
  certificate inputs can be rejected more strictly by the standard library.
- QUIC 0.59.1 and QPACK 0.6.0 bound decoded header and trailer sections;
  oversized headers may receive HTTP 431. Direct QUIC tracing and WebTransport
  API users must review upstream changes.
  When a router is hosted directly by an HTTP/3 server, the existing SSE
  writer's `Connection` header is invalid for HTTP/3; a loopback QUIC client
  rejects that SSE response. The same limitation exists on current main.
- x/net 0.62.0 tightens HTTP/2, IDNA, DNS and proxy handling. gRPC 1.83.2
  rejects malformed paths and authorities and changes resolver, flow-control
  and status handling. OAuth2 0.36.0 changes token and credential handling.
- OpenTelemetry 1.47 includes the 1.45 attribute, exporter and cardinality
  changes and fixes baggage parsing and attribute-length-limit handling.
  With a configured finite span attribute limit, values containing U+FFFD are
  now truncated at the limit instead of bypassing it. The log API and SDK are
  stable at 1.47; OTLP log exporters use 0.23. Collector configuration, custom
  instruments, dashboards and code depending on older telemetry APIs need their
  own migration checks.
- Updated AWS SDK defaults add S3 request checksums and response validation;
  custom S3-compatible endpoints must support the new behavior. Docker client
  changes affect test-harness APIs but do not update the host Docker or runc
  installation. Direct users of transitive Google, Docker, crypto or
  compression APIs must review their respective upstream compatibility notes.

The current main branch already includes the metric-v2 contract and the
request-context and binding-error changes. Review metric names, dimensions,
duration units and histogram dashboards when upgrading from older releases.
Bound handlers and input modifiers now receive the HTTP request context, not a
`*gin.Context`; Gin-only keys and casts must migrate as described below.
`NewErrorWithStatus` and `ErrorWithStatus` were removed; use `StatusCode` or an
`ErrorMapper` for application errors. Request-binding failures use
`BindingError` and map to HTTP 400 before application mappers or a wrapped
cause's status. Default/private 400 responses return `{"err":"bad request"}`
without error-provided headers; public privacy exposes the error details and
headers. Private 5xx responses retain their existing privacy behavior. Error
middleware does not overwrite a response that was already written or flushed.
These changes are inherited from main, not introduced by the dependency
security update.

Before the Gosoline v0.69.0 update, the refresh was exercised with real
loopback HTTP/1.1, HTTP/2 over TLS, and HTTP/3 over QUIC requests, JSON
validation, JWT acceptance and rejection, OAuth token exchange, and an owned
Docker container managed by Dockertest. Those results describe the earlier
v0.63.5 graph, not the current dependency graph.

The S3 SDK retains its new checksum and response-validation defaults.
AWS config v1.29.0 is the first release that loads both `when_supported`
defaults; an owned MinIO integration exercised a checksummed upload and
validated download without compatibility overrides on the earlier graph.

The selected Gosoline revision and OpenTelemetry SDK both use resource schema
1.43.0. The provider regression test constructs the actual Gosoline tracer and
metric writer, exports a span and a metric with a string dimension over local
OTLP HTTP and gRPC, and checks that both carry this schema. A separate
exporter test checks nested map attributes and an integer histogram over both.
These local tests do not validate an external collector's TLS, authentication,
retry or deployment configuration.

Earlier full-suite results on SDK candidates `1bc0b85` and `dd575e2` are
historical evidence for those graphs, not for the currently selected graph.

The earlier released Gosoline v0.63.5 OpenTelemetry provider was also started
with the updated SDK and exported a real span to an owned OpenTelemetry
Collector. Its resource schema was preserved, and flush and shutdown succeeded.

This is not a claim that the entire Gosoline dependency graph is free of
advisories. An October 2026 source scan of the earlier v0.63.5 graph using Go
1.26.10 reported `GO-2026-4518` (`pgproto3/v2`), `GO-2026-5004` (`pgx/v4`),
and `GO-2026-5046`, `GO-2026-5047`, `GO-2026-5048` (`hamba/avro/v2`)
through the earlier graph's database and test-environment dependencies. The
selected Gosoline revision migrates away from the affected pgx/v4 and
`hamba/avro/v2` paths, but its Avro fork still has no default map-allocation
limit. Do not treat a clean source scan as evidence that this allocation risk
or the entire transitive dependency graph is resolved. `GO-2026-5932` was a
module-only finding for the unused `x/crypto/openpgp` packages.

## Quick Start

Minimal example (from `examples/http-bind`):

```go
package main

import (
    "context"
    "fmt"

    "github.com/gosoline-project/httpserver"
    "github.com/justtrackio/gosoline/pkg/cfg"
    "github.com/justtrackio/gosoline/pkg/log"
)

func main() {
    httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
        router.HandleWith(httpserver.With(NewHandler, func(router *httpserver.Router, h *Handler) {
            router.POST("/a", httpserver.Bind(h.HandleA))
            router.GET("/b", httpserver.Bind(h.HandleB))
            router.GET("/err", httpserver.BindN(h.HandleErr))
        }))
        return nil
    })
}

type InputA struct {
    Id   int    `json:"id"`
    Name string `json:"name"`
}

// B is a plain string bound from body or query depending on tags.
type InputB string

type Handler struct{}

func NewHandler(ctx context.Context, config cfg.Config, logger log.Logger) (*Handler, error) {
    return &Handler{}, nil
}

func (h *Handler) HandleA(ctx context.Context, in *InputA) (map[string]any, error) {
    return map[string]any{"message": "Hello from A", "input": in}, nil
}

func (h *Handler) HandleB(ctx context.Context, in *InputB) (map[string]any, error) {
    return map[string]any{"message": "Hello from B", "input": in}, nil
}

func (h *Handler) HandleErr(ctx context.Context) (httpserver.Response, error) {
    return nil, fmt.Errorf("some error happened")
}
```

Run:
```bash
go run ./examples/http-bind
```

Sample request:
```bash
curl -X POST -H 'Content-Type: application/json' localhost:8080/a -d '{"id":1,"name":"alice"}'
```

## Binding

Use struct tags to opt-in to sources:

| Tag      | Source                |
|----------|-----------------------|
| `json`   | JSON body             |
| `form`   | Form / URL-encoded    |
| `header` | HTTP headers          |
| `uri`    | Path parameters       |
| `xml`    | XML body              |
| `yaml`   | YAML body             |
| `protobuf` | Protobuf body       |
| `msgpack` | MsgPack body         |
| `toml`   | TOML body             |

Bind variants:

- `Bind(func(ctx context.Context, input *T) (O, error))`
- `BindR(func(ctx context.Context, req *http.Request, input *T) (O, error))` (access raw *http.Request)
- `BindN(func(ctx context.Context) (O, error))` (no input)
- `BindNR(func(ctx context.Context, req *http.Request) (O, error))`

All `Bind` and `BindSse` variants, and custom input modifiers, receive the current
HTTP request context. This preserves request values, deadlines, and cancellation
without retaining Gin's pooled context. It does not extend the request lifetime.

Store application values in `Request.Context()`, not with `gin.Context.Set`.
Do not cast the handler context to `*gin.Context` or use Gin's special context
keys. Bind URL parameters with `uri` tags; use an `R` variant for raw request access.
Gin middleware and response rendering still use the Gin context directly.

The public handler abstractions use the same input/output type parameters:

```go
type Handler[I, O any] interface {
    Handle(context.Context, *I) (O, error)
}

type HandlerFunc[I, O any] func(context.Context, *I) (O, error)
```

The additional `O` parameter is a breaking API change from the former
`Handler[I]` and `HandlerFunc[I]` declarations. Migrate existing declarations by
adding their return type, for example `Handler[Input, Output]`. Handlers that
return explicit responses should use `Handler[Input, httpserver.Response]`. The
generated mock uses the same arity: `mocks.NewHandler[Input, Output](t)`.

`O` can be any value. The server negotiates and encodes ordinary values using
`Accept`, with JSON as the default representation. Return an explicit
`Response` when the handler needs to control the status, headers, content type,
or body directly.

## Responses

Typed handler results are encoded according to the request's `Accept` header.
The built-in server configures JSON by default:

```go
func (h *Handler) Handle(ctx context.Context, input *Input) (Output, error) {
    return Output{Ok: true}, nil
}
```

Ordinary outputs may also implement `httpserver.StatusCode` and
`httpserver.HeaderProvider`. The status and headers are applied to the
negotiated response while the body remains encoded by the selected
representation:

```go
type Output struct {
    Message string `json:"message"`
}

func (Output) StatusCode() int { return http.StatusAccepted }

func (Output) Header() http.Header {
    return http.Header{"X-Source": {"handler"}}
}
```

The negotiated `Content-Type` remains authoritative for ordinary outputs so
headers cannot disagree with the encoded representation. Other supplied
headers are merged into the response, and `Vary` values are combined.

`Response` values remain the full response escape hatch. They control status,
headers, content type, and body directly.

To configure additional representations for all routes on a built-in server,
pass a negotiator to `NewServer` or `NewServerWithSettings`:

```go
negotiator, err := httpserver.NewContentNegotiator(
    httpserver.ContentTypeApplicationJson,
    httpserver.JSONRepresentation(),
    httpserver.XMLRepresentation(),
)
if err != nil {
    return err
}

factory := httpserver.NewServer(
    "default",
    Factory,
    httpserver.WithResponseNegotiator(negotiator),
)
```

When using the application helper, pass server options separately from
application options. The remaining arguments, when needed, are
`application.Option` values:

```go
httpserver.RunServerWithOptions(
    Factory,
    []httpserver.ServerOption{
        httpserver.WithResponseNegotiator(negotiator),
    },
)
```

For a route or router-group-specific override, install the middleware on that
router instead:

```go
router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
```

The middleware applies to typed results and to default responses produced by
`ErrorMiddleware`. The built-in server installs a JSON-only negotiator by
default, so an unsupported success `Accept` value returns `406 Not Acceptable`.
Error responses are negotiated in the same way and fall back to JSON if the
requested representation cannot be selected or encoded.

Return an explicit `Response` when the handler needs direct HTTP response
control:

```go
httpserver.NewResponse(httpserver.WithBody([]byte("raw")), httpserver.WithStatusCode(201))
httpserver.NewTextResponse("hello world")
httpserver.NewJsonResponse(struct{Ok bool}{true})
httpserver.NewStatusResponse(http.StatusNoContent)
```

Explicit `Response` values are written as-is and do not participate in content
negotiation. Error responses use the configured negotiator as well; if the
requested representation cannot encode the error, the middleware falls back to
JSON so it can still return the mapped status code.

Options:

- `WithBody([]byte)`
- `WithHeader(key,value)` / `WithHeaders(http.Header)`
- `WithStatusCode(int)`

Configure a custom error handler with `WithErrorHandler`. The package-level setter is no longer available. The option applies to one server and does not change other servers:

```go
factory := httpserver.NewServer(
    "default",
    Factory,
    httpserver.WithErrorHandler(func(statusCode int, err error) any {
        return struct {
            Error string `json:"error"`
        }{Error: err.Error()}
    }),
)
```

Pass the option to `NewServer`, `NewServerWithSettings`, or
`RunServerWithOptions`. The handler should return an ordinary body value so the
configured negotiator can encode it. A non-nil handler is required.

For standalone Gin middleware, pass the same handler to the error and recovery
middleware:

```go
handler := func(statusCode int, err error) any {
    return struct {
        Error string `json:"error"`
    }{Error: err.Error()}
}
r.Use(httpserver.ErrorMiddlewareWithHandler(httpserver.ErrorsSettings{}, handler))
r.Use(httpserver.RecoveryWithSentryAndErrorHandler(logger, handler))
```

The mapped `statusCode` is applied by the middleware. An error can implement
`StatusCode` to carry an explicit HTTP status code, and can implement `Header()`
to provide HTTP headers. Error headers are applied to ordinary negotiated error
bodies.
`BindingError` marks request-binding failures and implements `StatusCode` with
HTTP 400. Its status takes precedence over application mappers and status codes
on its wrapped cause. With
default or private privacy, the middleware returns a generic `bad request` body
and does not propagate headers from the original error. Public privacy exposes
the error details and headers. Private 5xx errors use a generic internal server
error body and do not propagate headers from the original error.
`NewErrorWithStatus` and the `ErrorWithStatus` interface have been removed.
Use `StatusCode` or `ErrorMapper` for application errors, and `BindingError`
for request-binding failures.
Returning an explicit `Response` remains an escape hatch for cases that need to
control the status, headers, content type, or body directly; such responses
bypass content negotiation.

If a handler has already written, committed, or flushed a response, the error
middleware leaves it unchanged rather than rendering another error response.
Gin context errors are not cleared and remain available for logging.

`WithErrorMapper` adds a status mapper to one server. Pass the option to
`NewServer`, `NewServerWithSettings`, or `RunServerWithOptions`. Explicit status
errors that implement `StatusCode` take precedence over mappers. Mappers run in
option order before built-in validation and default mappings.
The former process-wide `RegisterErrorMapper` API is not available; migrate
registrations to server options.

The server's built-in recovery and overload responses use dedicated response
types and the server-level negotiator. Health-check responses use a map because
module names are dynamic, so a representation such as XML may not be able to
encode them and can fall back to JSON. A `Router.Use` override applies to routes
registered on that router and their negotiated errors. Explicit headers, such as
`Retry-After`, remain unchanged. If a selected encoder fails, the error path
preserves the mapped status and uses JSON as a fallback.

## Middleware

Attach gin-compatible handlers:

```go
r := gin.New()
r.Use(httpserver.LoggingMiddleware(logger, settings))
r.Use(httpserver.ErrorMiddleware())
r.Use(httpserver.RecoveryWithSentry(logger))
```

## Testing

Use the included helpers for unit-style handler tests:

```go
resp := httpserver.HttpTest(http.MethodPost, "/path", "/path", `{"x":1}`, handler,
    func(r *http.Request){ r.Header.Set("Content-Type", "application/json") })
```

Table-driven tests for binding and responses are provided in the repository as examples.

HTTP server test cases must provide routes through
`TestingSuiteHttpServerRouterAware`. To configure the test server, also implement
the optional `TestingSuiteHttpServerOptionsAware` interface:

```go
func (s *HttpSuite) SetupHttpServerOptions() []httpserver.ServerOption {
	return []httpserver.ServerOption{
		httpserver.WithErrorHandler(func(_ int, err error) any {
			return map[string]string{"error": err.Error()}
		}),
	}
}
```

The runner calls `SetupHttpServerOptions` after `SetT` and passes the returned
options to `NewServer`.

## Router Factories

You can modularize route registration:

```go
func Factory(ctx context.Context, cfg cfg.Config, log log.Logger, root *httpserver.Router) error {
    api := root.Group("api")
    api.GET("/health", httpserver.BindN(func(ctx context.Context) (map[string]string, error) {
        return map[string]string{"status":"ok"}, nil
    }))
    return nil
}
```
Register via `router.HandleWith` if using dynamic factories.

Handlers created with `httpserver.With` can implement `io.Closer`. The server calls `Close` after it stops serving requests. `Close` must block until cleanup completes and must be idempotent.

## Contributing

Pull requests welcome. Please include tests for new functionality and keep changes minimal.

## Mock generation

Run `mise exec -- go generate -run mockery ./...` from the repository root to regenerate mocks using the single `.mockery.yml` package configuration (`all: true`, recursive).
The full command, `mise exec -- go generate ./...`, also runs the existing protobuf directive and writes outside this checkout to `../suite/testdata`; use it only when that location is intended.

## License

MIT (see LICENSE file if present).
