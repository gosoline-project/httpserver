package httpserver

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/justtrackio/gosoline/pkg/validation"
)

const (
	// ErrorPrivacyPublic exposes error messages to clients.
	ErrorPrivacyPublic = "public"
	// ErrorPrivacyPrivate hides internal server error details from clients.
	ErrorPrivacyPrivate = "private"
)

// ErrorHandler converts an error and status code into a response body.
// Returning a Response remains an escape hatch for callers that need to control
// the response directly; ordinary values are rendered by response negotiation.
type ErrorHandler func(statusCode int, err error) any

// ErrorMapper maps an application error to an HTTP status code. The handled
// result indicates whether the mapper applies to the error.
type ErrorMapper func(err error) (statusCode int, handled bool)

func headersFromError(err error) http.Header {
	var headerProvider HeaderProvider
	if errors.As(err, &headerProvider) {
		return headerProvider.Header()
	}

	return nil
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

// errorResponseBody keeps the default JSON and XML error representations
// stable when ordinary error bodies are negotiated.
type errorResponseBody struct {
	XMLName xml.Name `xml:"error" json:"-"`
	Err     string   `json:"err" xml:"err"`
}

// WithErrorHandler replaces the package-level error body handler.
func WithErrorHandler(handler ErrorHandler) {
	defaultErrorHandler = handler
}

// GetErrorStatusCode returns the HTTP status code mapped from err, or 500 otherwise.
func GetErrorStatusCode(err error) int {
	return getErrorStatusCode(err)
}

// GetErrorStatusCodeWithMappers returns the HTTP status code mapped from err
// with the provided mappers, or 500 otherwise.
func GetErrorStatusCodeWithMappers(err error, mappers ...ErrorMapper) int {
	return getErrorStatusCode(err, mappers...)
}

func getErrorStatusCode(err error, mappers ...ErrorMapper) int {
	var errWithStatus ErrorWithStatus
	if errors.As(err, &errWithStatus) {
		return errWithStatus.StatusCode()
	}

	if statusCode, handled := errorStatusCodeFromMappers(err, mappers); handled {
		return statusCode
	}

	if validation.IsValidationError(err) {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

func errorStatusCodeFromMappers(err error, mappers []ErrorMapper) (int, bool) {
	for _, mapper := range mappers {
		if mapper == nil {
			continue
		}

		if statusCode, handled := mapper(err); handled {
			return statusCode, true
		}
	}

	return 0, false
}

var defaultErrorHandler ErrorHandler = func(_ int, err error) any {
	return errorResponseBody{Err: err.Error()}
}
