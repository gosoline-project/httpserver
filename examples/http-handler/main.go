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
			if _, err := ctx.Writer.WriteString("bla"); err != nil {
				ginErr := ctx.Error(err)
				ginErr.Type = gin.ErrorTypePrivate
			}
		})

		grp := router.Group("grp")
		grp.GET("/bla", func(ctx *gin.Context) {
			if _, err := ctx.Writer.WriteString("grouped bla"); err != nil {
				ginErr := ctx.Error(err)
				ginErr.Type = gin.ErrorTypePrivate
			}
		})

		router.GET("/blocking", func(ctx *gin.Context) {
			timer := clock.NewRealTimer(time.Second * 5)

			select {
			case <-ctx.Done():
				logger.Info(ctx, "context done")
			case <-timer.Chan():
				logger.Info(ctx, "timer done")
			}

			if _, err := ctx.Writer.WriteString("done"); err != nil {
				ginErr := ctx.Error(err)
				ginErr.Type = gin.ErrorTypePrivate
			}
		})

		return nil
	})
}
