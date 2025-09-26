package httpserver

import (
	"github.com/gin-gonic/gin"
)

type ErrorHandler func(statusCode int, err error) Response

func errorHandlerJson(statusCode int, err error) Response {
	return NewJsonResponse(gin.H{"err": err.Error()}, WithStatusCode(statusCode))
}

func WithErrorHandler(handler ErrorHandler) {
	defaultErrorHandler = handler
}

func GetErrorHandler() ErrorHandler {
	return defaultErrorHandler
}

var defaultErrorHandler = errorHandlerJson
