package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.HandleWith(NewHandler, func(router *httpserver.Router, s *Handler) {
			router.POST("/a", s.HandleA)
			router.GET("/b", s.HandleB)
			router.Handle(http.MethodGet, "/err", httpserver.BindN(s.HandleErr))
		})

		return nil
	})
}

type InputA struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
}
type (
	InputB  string
	Handler struct{}
)

func NewHandler(ctx context.Context, config cfg.Config, logger log.Logger) (*Handler, error) {
	return &Handler{}, nil
}

func (r *Handler) HandleA(ctx context.Context, _ *http.Request, input *InputA) (map[string]any, error) {
	return map[string]any{
		"message": "Hello from A",
		"input":   *input,
	}, nil
}

func (r *Handler) HandleB(ctx context.Context, _ *http.Request, input *InputB) (map[string]any, error) {
	return map[string]any{
		"message": "Hello from B",
		"input":   *input,
	}, nil
}

func (r *Handler) HandleErr(ctx context.Context) (httpserver.Response, error) {
	return nil, fmt.Errorf("some error happened")
}
