package httpserver

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/exec"
	"github.com/justtrackio/gosoline/pkg/log"
)

// RecoveryWithSentry recovers panics, logs them, and returns a negotiated 500 response.
func RecoveryWithSentry(logger log.Logger) gin.HandlerFunc {
	return (&HttpServer{}).RecoveryWithSentry(logger)
}

// RecoveryWithSentry recovers panics, logs them, and returns a negotiated 500
// response using this HTTP server's error handler.
func (s *HttpServer) RecoveryWithSentry(logger log.Logger) gin.HandlerFunc {
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
				rerr = errors.New("unknown panic")
			}

			logger.Error(ctx, "%w", rerr)
			c.Abort()
			writeErrorResponse(c, s, http.StatusInternalServerError, rerr)
		}()

		c.Next()
	}
}
