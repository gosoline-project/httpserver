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

Custom error handlers should normally return an ordinary body value so the
configured negotiator can encode it:

```go
httpserver.WithErrorHandler(func(statusCode int, err error) any {
    return struct {
        Error string `json:"error"`
    }{Error: err.Error()}
})
```

`WithErrorHandler` assigns the provided handler directly. Do not pass `nil`; use a
non-nil handler for every error response.

The mapped `statusCode` is applied by the middleware. Returning an explicit
`Response` remains an escape hatch for cases that need to control the status,
headers, content type, or body directly; such responses bypass content
negotiation.

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

## Contributing

Pull requests welcome. Please include tests for new functionality and keep changes minimal.

## License

MIT (see LICENSE file if present).
