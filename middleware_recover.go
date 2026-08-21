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
	return RecoveryWithSentryAndErrorHandler(logger, defaultErrorHandler)
}

// RecoveryWithSentryAndErrorHandler recovers panics and uses the provided error handler.
func RecoveryWithSentryAndErrorHandler(logger log.Logger, handler ErrorHandler) gin.HandlerFunc {
	if handler == nil {
		panic("error handler is required")
	}

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
			writeErrorResponseWithHandler(c, http.StatusInternalServerError, rerr, handler)
		}()

		c.Next()
	}
}
