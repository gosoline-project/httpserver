package httpserver

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func ErrorMiddleware() gin.HandlerFunc {
	return ErrorMiddlewareWithSettings(ErrorsSettings{})
}

func ErrorMiddlewareWithSettings(settings ErrorsSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		statusCode := GetErrorStatusCode(err)

		if statusCode >= 500 && (settings.Privacy == ErrorPrivacyPrivate || settings.Privacy == "") {
			err = fmt.Errorf("internal server error")
		}

		errorHandler := GetErrorHandler()
		response := errorHandler(statusCode, err)

		if err = BindHandleResponse(response, c); err != nil {
			c.Errors = append(c.Errors, &gin.Error{Err: fmt.Errorf("error response error: %w", err), Type: gin.ErrorTypePrivate})
		}
	}
}
