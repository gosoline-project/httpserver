package httpserver

import (
	"github.com/gin-gonic/gin"
)

type ErrorHandler func(statusCode int, err error) Response

func errorHandlerJson(statusCode int, err error) Response {
	body := gin.H{"err": err.Error()}
	if statusCode >= 500 {
		body = gin.H{"err": "internal server error"}
	}

	return NewJsonResponse(body, WithStatusCode(statusCode))
}

func WithErrorHandler(handler ErrorHandler) {
	defaultErrorHandler = handler
}

func GetErrorHandler() ErrorHandler {
	return defaultErrorHandler
}

var defaultErrorHandler = errorHandlerJson
