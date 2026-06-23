package httpserver

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/validation"
)

const (
	// ErrorPrivacyPublic exposes error messages to clients.
	ErrorPrivacyPublic = "public"
	// ErrorPrivacyPrivate hides internal server error details from clients.
	ErrorPrivacyPrivate = "private"
)

// ErrorHandler converts an error and status code into an HTTP response.
type ErrorHandler func(statusCode int, err error) Response

// ErrorWithStatus is an error that carries an explicit HTTP status code.
type ErrorWithStatus interface {
	error
	StatusCode() int
}

type errorWithStatus struct {
	statusCode int
	err        error
}

// NewErrorWithStatus wraps an error with an HTTP status code for the error middleware.
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
	return NewJsonResponse(gin.H{"err": err.Error()}, WithStatusCode(statusCode))
}

// WithErrorHandler replaces the package-level default error response handler.
func WithErrorHandler(handler ErrorHandler) {
	defaultErrorHandler = handler
}

// GetErrorHandler returns the package-level default error response handler.
func GetErrorHandler() ErrorHandler {
	return defaultErrorHandler
}

// GetErrorStatusCode returns the HTTP status code carried by err, or 500 otherwise.
func GetErrorStatusCode(err error) int {
	var errWithStatus ErrorWithStatus
	if errors.As(err, &errWithStatus) {
		return errWithStatus.StatusCode()
	}

	if validation.IsValidationError(err) {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

var defaultErrorHandler = errorHandlerJson
