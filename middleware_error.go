package httpserver

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// ErrorMiddleware creates error middleware with default settings and a private
// default error configuration.
func ErrorMiddleware() gin.HandlerFunc {
	return (&HttpServer{}).ErrorMiddleware()
}

// ErrorMiddlewareWithSettings creates error middleware with a private default
// error configuration. Use HttpServer.ErrorMiddlewareWithSettings to use the
// configuration of a specific HTTP server instance.
func ErrorMiddlewareWithSettings(settings ErrorsSettings) gin.HandlerFunc {
	return (&HttpServer{}).ErrorMiddlewareWithSettings(settings)
}

// ErrorMiddleware creates error middleware with default settings for this HTTP server.
func (s *HttpServer) ErrorMiddleware() gin.HandlerFunc {
	return s.ErrorMiddlewareWithSettings(ErrorsSettings{})
}

// ErrorMiddlewareWithSettings converts Gin context errors into HTTP error responses for this HTTP server.
func (s *HttpServer) ErrorMiddlewareWithSettings(settings ErrorsSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		statusCode := s.GetErrorStatusCode(err)

		if statusCode >= 500 && (settings.Privacy == ErrorPrivacyPrivate || settings.Privacy == "") {
			err = fmt.Errorf("internal server error")
		}

		writeErrorResponse(c, s, statusCode, err)
	}
}

func writeErrorResponse(c *gin.Context, server *HttpServer, statusCode int, err error) {
	server.errorHandlerMu.RLock()
	handler := server.errorHandler
	server.errorHandlerMu.RUnlock()
	if handler == nil {
		handler = defaultErrorHandler
	}

	writeNegotiatedResponse(c, handler(statusCode, err), statusCode)
}

func writeNegotiatedResponse(c *gin.Context, output any, statusCode int) {
	response, responseErr := responseFromOutputWithStatus(c, output, statusCode)
	if responseErr != nil {
		// If the requested representation cannot encode the response, use JSON
		// as a last resort so the client still receives the mapped status.
		response = NewJsonResponse(
			output,
			WithStatusCode(statusCode),
			WithHeader(HeaderVary, HeaderAccept),
		)
	}

	if err := BindHandleResponse(response, c); err != nil {
		c.Errors = append(c.Errors, &gin.Error{Err: fmt.Errorf("error response error: %w", err), Type: gin.ErrorTypePrivate})
	}
}
