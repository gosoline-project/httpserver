package httpserver

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func ErrorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		statusCode := GetErrorStatusCode(err)
		response := GetErrorHandler()(statusCode, err)

		if err = BindHandleResponse(response, c); err != nil {
			_ = c.Error(fmt.Errorf("error response error: %w", err))
		}
	}
}
