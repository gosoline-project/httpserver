package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBodySizeMiddleware limits incoming request bodies. A value <= 0 disables the limit.
func MaxBodySizeMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}

		c.Next()
	}
}
