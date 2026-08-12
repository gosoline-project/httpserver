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

// WithErrorHandler replaces the error body handler for this HTTP server.
func (s *HttpServer) WithErrorHandler(handler ErrorHandler) {
	if handler == nil {
		handler = defaultErrorHandler
	}

	s.errorHandlerMu.Lock()
	s.errorHandler = handler
	s.errorHandlerMu.Unlock()
}

// RegisterErrorMapper adds a status mapper for this HTTP server. Mappers are
// evaluated in registration order after explicit ErrorWithStatus values and
// before built-in status mappings.
func (s *HttpServer) RegisterErrorMapper(mapper ErrorMapper) {
	if mapper == nil {
		panic("error mapper is required")
	}

	s.errorMappersMu.Lock()
	s.errorMappers = append(s.errorMappers, mapper)
	s.errorMappersMu.Unlock()
}

// GetErrorStatusCode returns the HTTP status code mapped from err for this
// server, or 500 otherwise.
func (s *HttpServer) GetErrorStatusCode(err error) int {
	var errWithStatus ErrorWithStatus
	if errors.As(err, &errWithStatus) {
		return errWithStatus.StatusCode()
	}

	if statusCode, handled := s.errorStatusCodeFromMappers(err); handled {
		return statusCode
	}

	if validation.IsValidationError(err) {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

func (s *HttpServer) errorStatusCodeFromMappers(err error) (int, bool) {
	s.errorMappersMu.RLock()
	mappers := append([]ErrorMapper(nil), s.errorMappers...)
	s.errorMappersMu.RUnlock()

	for _, mapper := range mappers {
		if statusCode, handled := mapper(err); handled {
			return statusCode, true
		}
	}

	return 0, false
}

func defaultErrorHandler(_ int, err error) any {
	return errorResponseBody{Err: err.Error()}
}
