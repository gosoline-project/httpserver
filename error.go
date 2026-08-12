package httpserver

import (
	"encoding/xml"
	"errors"
	"net/http"
	"sync"

	"github.com/justtrackio/gosoline/pkg/validation"
)

const (
	// ErrorPrivacyPublic exposes error messages to clients.
	ErrorPrivacyPublic = "public"
	// ErrorPrivacyPrivate hides internal server error details from clients.
	ErrorPrivacyPrivate = "private"
)

// ErrorHandler converts an error and status code into an explicit HTTP response.
type ErrorHandler func(statusCode int, err error) Response

// ErrorMapper maps an application error to an HTTP status code. The handled
// result indicates whether the mapper applies to the error.
type ErrorMapper func(err error) (statusCode int, handled bool)

var (
	errorMappersMu sync.RWMutex
	errorMappers   []ErrorMapper
)

// RegisterErrorMapper adds a status mapper used by GetErrorStatusCode.
// Mappers are evaluated in registration order after explicit ErrorWithStatus
// values and before built-in status mappings.
func RegisterErrorMapper(mapper ErrorMapper) {
	if mapper == nil {
		panic("error mapper is required")
	}

	errorMappersMu.Lock()
	defer errorMappersMu.Unlock()

	errorMappers = append(errorMappers, mapper)
}

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

type errorResponseBody struct {
	XMLName xml.Name `xml:"error" json:"-"`
	Err     string   `json:"err" xml:"err"`
}

func errorHandlerBody(_ int, err error) any {
	return errorResponseBody{Err: err.Error()}
}

func errorHandlerJson(statusCode int, err error) Response {
	return NewJsonResponse(errorResponseBody{Err: err.Error()}, WithStatusCode(statusCode))
}

// WithErrorHandler replaces the package-level default error response handler.
func WithErrorHandler(handler ErrorHandler) {
	if handler == nil {
		defaultErrorHandler = errorHandlerJson
		hasCustomErrorHandler = false

		return
	}

	defaultErrorHandler = handler
	hasCustomErrorHandler = true
}

// GetErrorHandler returns the package-level default error response handler.
func GetErrorHandler() ErrorHandler {
	return defaultErrorHandler
}

func errorHandlerOutput(statusCode int, err error) any {
	if !hasCustomErrorHandler {
		return errorHandlerBody(statusCode, err)
	}

	return defaultErrorHandler(statusCode, err)
}

// GetErrorStatusCode returns the HTTP status code mapped from err, or 500 otherwise.
func GetErrorStatusCode(err error) int {
	var errWithStatus ErrorWithStatus
	if errors.As(err, &errWithStatus) {
		return errWithStatus.StatusCode()
	}

	if statusCode, handled := errorStatusCodeFromMappers(err); handled {
		return statusCode
	}

	if validation.IsValidationError(err) {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

func errorStatusCodeFromMappers(err error) (int, bool) {
	errorMappersMu.RLock()
	mappers := append([]ErrorMapper(nil), errorMappers...)
	errorMappersMu.RUnlock()

	for _, mapper := range mappers {
		if statusCode, handled := mapper(err); handled {
			return statusCode, true
		}
	}

	return 0, false
}

var (
	defaultErrorHandler   ErrorHandler = errorHandlerJson
	hasCustomErrorHandler bool
)
