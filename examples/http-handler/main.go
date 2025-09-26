package main

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/clock"
	"github.com/justtrackio/gosoline/pkg/log"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.GET("/bla", func(ctx *gin.Context) {
			ctx.Writer.WriteString("bla")
		})

		grp := router.Group("grp")
		grp.GET("/bla", func(ctx *gin.Context) {
			ctx.Writer.WriteString("grouped bla")
		})

		router.GET("/blocking", func(ctx *gin.Context) {
			timer := clock.NewRealTimer(time.Second * 5)

			select {
			case <-ctx.Done():
				logger.Info(ctx, "context done")
			case <-timer.Chan():
				logger.Info(ctx, "timer done")
			}

			ctx.Writer.WriteString("done")
		})

		return nil
	})
}
