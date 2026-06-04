package httpserver

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorHandler func(statusCode int, err error) Response

type ErrorWithStatus interface {
	error
	StatusCode() int
}

type errorWithStatus struct {
	statusCode int
	err        error
}

func NewErrorWithStatus(statusCode int, err error) ErrorWithStatus {
	return &errorWithStatus{
		statusCode: statusCode,
		err:        err,
	}
}

func (e errorWithStatus) Error() string {
	return e.err.Error()
}

func (e errorWithStatus) StatusCode() int {
	return e.statusCode
}

func (e errorWithStatus) Unwrap() error {
	return e.err
}

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

func GetErrorStatusCode(err error) int {
	var errWithStatus ErrorWithStatus
	if errors.As(err, &errWithStatus) {
		return errWithStatus.StatusCode()
	}

	return http.StatusInternalServerError
}

var defaultErrorHandler = errorHandlerJson
