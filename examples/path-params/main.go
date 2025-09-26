package main

import (
	"context"

	"github.com/gofiber/fiber/v3"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.Get("/hello/:name", func(ctx fiber.Ctx) error {
			name := ctx.Params("name")
			ctx.Send([]byte("Hello, " + name))

			return nil
		})

		return nil
	})
}
