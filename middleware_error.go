package httpserver

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ErrorMiddleware creates error middleware with default settings.
func ErrorMiddleware() gin.HandlerFunc {
	return ErrorMiddlewareWithMappers(ErrorsSettings{})
}

// ErrorMiddlewareWithSettings converts Gin context errors into HTTP error responses.
func ErrorMiddlewareWithSettings(settings ErrorsSettings) gin.HandlerFunc {
	return ErrorMiddlewareWithMappers(settings)
}

// ErrorMiddlewareWithMappers converts Gin context errors into HTTP error responses
// with the provided application error mappers.
func ErrorMiddlewareWithMappers(settings ErrorsSettings, mappers ...ErrorMapper) gin.HandlerFunc {
	mappers = append([]ErrorMapper(nil), mappers...)

	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		errorHeaders := headersFromError(err)
		statusCode := GetErrorStatusCodeWithMappers(err, mappers...)

		if statusCode >= 500 && (settings.Privacy == ErrorPrivacyPrivate || settings.Privacy == "") {
			err = fmt.Errorf("internal server error")
			errorHeaders = nil
		}

		writeErrorResponseWithHeaders(c, statusCode, err, errorHeaders)
	}
}

func writeErrorResponse(c *gin.Context, statusCode int, err error) {
	writeErrorResponseWithHeaders(c, statusCode, err, headersFromError(err))
}

func writeErrorResponseWithHeaders(c *gin.Context, statusCode int, err error, errorHeaders http.Header) {
	output := defaultErrorHandler(statusCode, err)
	writeNegotiatedResponse(c, output, statusCode, errorHeaders)
}

func writeNegotiatedResponse(c *gin.Context, output any, statusCode int, errorHeaders ...http.Header) {
	var response Response
	var err error
	var providedErrorHeaders http.Header

	if len(errorHeaders) > 0 {
		providedErrorHeaders = errorHeaders[0]
	}

	if response, err = responseFromOutputWithStatusAndHeaders(c, output, statusCode, providedErrorHeaders); err != nil {
		// If the requested representation cannot encode the response, use JSON
		// as a last resort so the client still receives the mapped status.
		response = NewJsonResponse(
			output,
			WithStatusCode(statusCode),
			WithHeader(HeaderVary, HeaderAccept),
		)
		if headerProvider, ok := output.(HeaderProvider); ok {
			response = withHeaders(response, headerProvider.Header())
		}
		response = withHeaders(response, providedErrorHeaders)
	}

	if err = BindHandleResponse(response, c); err != nil {
		c.Errors = append(c.Errors, &gin.Error{Err: fmt.Errorf("error response error: %w", err), Type: gin.ErrorTypePrivate})
	}
}
