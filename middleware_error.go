package httpserver

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// ErrorMiddleware creates error middleware with default settings.
func ErrorMiddleware() gin.HandlerFunc {
	return ErrorMiddlewareWithSettings(ErrorsSettings{})
}

// ErrorMiddlewareWithSettings converts Gin context errors into HTTP error responses.
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

		writeErrorResponse(c, statusCode, err)
	}
}

func writeErrorResponse(c *gin.Context, statusCode int, err error) {
	output := errorHandlerOutput(statusCode, err)
	writeNegotiatedResponse(c, output, statusCode, errorResponseBody{Err: err.Error()})
}

func writeNegotiatedResponse(c *gin.Context, output any, statusCode int, fallback any) {
	response, responseErr := responseFromOutputWithStatus(c, output, statusCode)
	if responseErr != nil {
		// If the requested representation cannot encode the response, use JSON
		// as a last resort so the client still receives the mapped status.
		response = NewJsonResponse(
			fallback,
			WithStatusCode(statusCode),
			WithHeader(HeaderVary, HeaderAccept),
		)
	}

	if err := BindHandleResponse(response, c); err != nil {
		c.Errors = append(c.Errors, &gin.Error{Err: fmt.Errorf("error response error: %w", err), Type: gin.ErrorTypePrivate})
	}
}
