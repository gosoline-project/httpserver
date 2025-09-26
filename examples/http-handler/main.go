package main

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/clock"
	"github.com/justtrackio/gosoline/pkg/log"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.Get("/bla", func(ctx fiber.Ctx) error {
			ctx.Write([]byte("bla"))

			return nil
		})

		grp := router.Group("grp")
		grp.Get("/bla", func(ctx fiber.Ctx) error {
			ctx.Write([]byte("grouped bla"))

			return nil
		})

		router.Get("/blocking", func(ctx fiber.Ctx) error {
			timer := clock.NewRealTimer(time.Minute)

			select {
			case <-ctx.RequestCtx().Done():
				logger.Info(ctx, "context done")
			case <-timer.Chan():
				logger.Info(ctx, "timer done")
			}

			ctx.SendString("done")

			return nil
		})

		return nil
	})
}
