# httpserver

A lightweight, opinionated HTTP server helper library built on top of [gin](https://github.com/gin-gonic/gin) and the [gosoline](https://github.com/justtrackio/gosoline) ecosystem. It provides structured request binding, consistent response types, middleware (logging, recovery, compression, CORS), and an application-friendly router definition API for building modular services.

## Features

- Declarative routing with grouping and middleware chaining.
- Generic request binding: automatically bind JSON, form, query, headers, URI params, protobuf, XML, etc. using struct tags.
- Response abstractions: plain, status, JSON responses with fluent options (headers, status code).
- Middleware: logging, error handling, recovery, metrics, profiling, compression, CORS.
- Simple test helpers and suite integration.
- Composable router factories for modular service assembly.

## Installation

```bash
go get github.com/gosoline-project/httpserver
```

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

The mapped `statusCode` is applied by the middleware. Errors may implement
`StatusCode()` and `Header()`, which lets them provide both the error status and
HTTP headers. Error headers are applied to ordinary negotiated error bodies.
Private 5xx errors use the generic error body and do not propagate headers from
the original error. Returning an explicit `Response` remains an escape hatch
for cases that need to control the status, headers, content type, or body
directly; such responses bypass content negotiation.

`WithErrorMapper` adds a status mapper to one server. Pass the option to
`NewServer`, `NewServerWithSettings`, or `RunServerWithOptions`. Mappers run in
option order after an explicit `NewErrorWithStatus` and before the built-in
validation and default mappings. The former process-wide `RegisterErrorMapper`
API is not available; migrate registrations to server options.

The server's built-in recovery and overload responses use dedicated response
types and the server-level negotiator. Health-check responses use a map because
module names are dynamic, so a representation such as XML may not be able to
encode them and can fall back to JSON. A `Router.Use` override applies to routes
registered on that router and their negotiated errors. Explicit headers, such as
`Retry-After`, remain unchanged. If a selected encoder fails, the error path
preserves the mapped status and uses JSON as a fallback.

## WebSockets

WebSockets are a streaming transport, separate from typed HTTP responses. Register a route on an `HttpServer` created by `NewServer` or `NewServerWithSettings` (or the public `NewWithInterfaces` constructor); a bare Gin router has no managed connection registry and returns HTTP 500 before an upgrade.

Configure the connection limits and origin policy with a server option:

```go
httpserver.WithWebsocketOptions(httpserver.WebsocketOptions{
    ReadLimit:    2 << 20,
    WriteTimeout: 5 * time.Second,
    CloseTimeout: time.Second,
    CheckOrigin:  func(r *http.Request) bool { return r.Header.Get("Origin") == "https://app.example.com" },
})
```

`WebsocketOptions` contains `ReadLimit int64`, `WriteTimeout time.Duration`, `CloseTimeout time.Duration`, and `CheckOrigin func(*http.Request) bool`. Zero values use a 1 MiB read limit, 5-second write timeout, 1-second close timeout, and Gorilla's default origin check: same-host origins are accepted, as are requests without an `Origin` header. Set `CheckOrigin` explicitly to change that policy; CORS settings do not grant WebSocket origins. Negative limits or timeouts are invalid.

Register one of the four callback binders with `Router.GET`:

```go
func BindWebsocket[I any](handler func(context.Context, *I, *WebsocketConn) error, binders ...binding.Binding) gin.HandlerFunc
func BindWebsocketR[I any](handler func(context.Context, *http.Request, *I, *WebsocketConn) error, binders ...binding.Binding) gin.HandlerFunc
func BindWebsocketN(handler func(context.Context, *WebsocketConn) error) gin.HandlerFunc
func BindWebsocketNR(handler func(context.Context, *http.Request, *WebsocketConn) error) gin.HandlerFunc
```
Typed variants bind request input before upgrading and return binding failures through the normal negotiated HTTP 400 path; `R` variants also pass the raw request.

For example, use the same `RouterFactory` and `NewServer` registration as other routes:

```go
func websocketRoutes(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
    router.GET("/ws", httpserver.BindWebsocketN(echoWebsocket))
    return nil
}

var serverFactory = httpserver.NewServer(
    "api",
    websocketRoutes,
    httpserver.WithWebsocketOptions(httpserver.WebsocketOptions{}),
)
```

`WebsocketConn` exposes complete text and binary messages:

```go
type WebsocketMessageType int

const (
    WebsocketTextMessage   WebsocketMessageType = 1
    WebsocketBinaryMessage WebsocketMessageType = 2
)

func (*WebsocketConn) ReadMessage() (WebsocketMessageType, []byte, error)
func (*WebsocketConn) Send(data []byte) error
func (*WebsocketConn) SendBinary(data []byte) error
func (*WebsocketConn) Close() error
func (*WebsocketConn) CloseWithStatus(code int, reason string) error
func (*WebsocketConn) RejectIncomingMessages(reason string) error
```

A duplex handler owns the single application read loop; concurrent reads are rejected. Concurrent `Send` and `SendBinary` calls are serialized, with a fresh write deadline for each send. `Send` sends one text message, including an empty text message for a nil or empty slice; `SendBinary` sends one binary message.

```go
func echoWebsocket(ctx context.Context, conn *httpserver.WebsocketConn) error {
    for {
        messageType, data, err := conn.ReadMessage()
        if err != nil {
            return err
        }

        switch messageType {
        case httpserver.WebsocketTextMessage:
            if err := conn.Send(data); err != nil {
                return err
            }
        case httpserver.WebsocketBinaryMessage:
            if err := conn.SendBinary(data); err != nil {
                return err
            }
        }
    }
}
```

For an outbound-only stream, call `RejectIncomingMessages` before starting any application read. It starts the exclusive framework reader, processes control frames, and closes on the first incoming data message with status 1008 and the supplied reason; it does not wait for a fragmented message body. It may be called only once and cannot be combined with `ReadMessage`.

```go
func serveFeed(ctx context.Context, conn *httpserver.WebsocketConn) error {
    if err := conn.RejectIncomingMessages("server stream does not accept messages"); err != nil {
        return err
    }
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            if err := conn.Send([]byte(`{"status":"ok"}`)); err != nil {
                return err
            }
        }
    }
}
```

The handler runs synchronously with a connection-lifetime context that preserves Gin context values and observes cancellation of the underlying request; terminal connection I/O failures also cancel it. The callback owns any goroutines it starts and must stop and join them before returning; do not retain or use the Gin context asynchronously. Framework-owned workers are joined during binder cleanup. A clean callback return closes normally with 1000. An unexpected callback error closes with 1011 (`"stream failed"`).

`Close` is idempotent and selects a normal 1000 close. `CloseWithStatus` accepts server-sent protocol codes 1000–1003, 1007–1009, and 1011–1014, or application codes 3000–4999. Close reasons must be valid UTF-8 and at most 123 bytes. Invalid codes or reasons return a descriptive error and leave the connection usable. `RejectIncomingMessages` applies the same reason validation before changing receive mode. The first selected close status wins.

Protocol/framing errors and the configured read limit take precedence over the receive-rejection policy: malformed frames retain status 1002, and messages exceeding the read limit retain 1009 rather than being reported as 1008.

On shutdown, managed WebSockets receive a best-effort 1001 (`"server shutting down"`) close and are hard-closed within the shared shutdown budget, bounded by `CloseTimeout`. Shutdown cancels callbacks and waits for them before normally running registered application handler closers. If the budget expires first, shutdown reports an error and still attempts those closers; it cannot forcibly stop a callback that ignores cancellation or blocks indefinitely.

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

## License

MIT (see LICENSE file if present).
