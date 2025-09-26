package httpserver

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/exec"
	"github.com/justtrackio/gosoline/pkg/log"
)

func RecoveryWithSentry(logger log.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			var rerr error

			ctx := c.Request.Context()
			err := recover()

			switch rval := err.(type) {
			case nil:
				return
			case error:
				if exec.IsConnectionError(rval) {
					logger.Warn(ctx, "connection error: %s", rval.Error())

					return
				}

				rerr = rval
			case string:
				rerr = errors.New(rval)
			default:
				c.AbortWithStatus(http.StatusInternalServerError)
			}

			logger.Error(ctx, "%w", rerr)
			c.JSON(http.StatusInternalServerError, gin.H{"err": rerr.Error()})
		}()

		c.Next()
	}
}
