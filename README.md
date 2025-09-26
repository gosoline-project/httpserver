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

func (h *Handler) HandleA(ctx context.Context, in *InputA) (httpserver.Response, error) {
    return httpserver.NewJsonResponse(map[string]any{"message": "Hello from A", "input": in}), nil
}

func (h *Handler) HandleB(ctx context.Context, in *InputB) (httpserver.Response, error) {
    return httpserver.NewJsonResponse(map[string]any{"message": "Hello from B", "input": in}), nil
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

- `Bind(func(ctx context.Context, input *T) (Response, error))`
- `BindR(func(ctx context.Context, req *http.Request, input *T) (Response, error))` (access raw *http.Request)
- `BindN(func(ctx context.Context) (Response, error))` (no input)
- `BindNR(func(ctx context.Context, req *http.Request) (Response, error))`

## Responses

```go
httpserver.NewResponse(WithBody([]byte("raw")), WithStatusCode(201))
httpserver.NewTextResponse("hello world")
httpserver.NewJsonResponse(struct{Ok bool}{true})
httpserver.NewStatusResponse(http.StatusNoContent)
```

Options:

- `WithBody([]byte)`
- `WithHeader(key,value)` / `WithHeaders(http.Header)`
- `WithStatusCode(int)`

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
    api.GET("/health", httpserver.BindN(func(ctx context.Context) (httpserver.Response, error) {
        return httpserver.NewJsonResponse(map[string]string{"status":"ok"}), nil
    }))
    return nil
}
```
Register via `router.HandleWith` if using dynamic factories.

## Contributing

Pull requests welcome. Please include tests for new functionality and keep changes minimal.

## License

MIT (see LICENSE file if present).
