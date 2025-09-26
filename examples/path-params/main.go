package main

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

func main() {
	httpserver.RunDefaultServer(func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.GET("/hello/:name", func(ginCtx *gin.Context) {
			name := ginCtx.Param("name")
			ginCtx.Writer.WriteString("Hello, " + name)
		})

		return nil
	})
}
