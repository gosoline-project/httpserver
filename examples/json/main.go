package main

import (
	"context"

	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/valyala/fasthttp"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.HandleWith(httpserver.With(NewHandler, func(router *httpserver.Router, s *Handler) {
			router.Post("/a", httpserver.Bind(s.HandleA))
			router.Get("/b", httpserver.Bind(s.HandleB))
		}))

		return nil
	})
}

type InputA struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
}
type InputB string
type Handler struct{}

func NewHandler(ctx context.Context, config cfg.Config, logger log.Logger) (*Handler, error) {
	return &Handler{}, nil
}

func (r *Handler) HandleA(ctx context.Context, req *fasthttp.Request, input *InputA) (httpserver.Response, error) {
	return httpserver.NewJsonResponse(map[string]any{
		"message": "Hello from A",
		"input":   *input,
	}), nil
}

func (r *Handler) HandleB(ctx context.Context, req *fasthttp.Request, input *InputB) (httpserver.Response, error) {
	return httpserver.NewJsonResponse(map[string]any{
		"message": "Hello from B",
		"input":   *input,
	}), nil
}
