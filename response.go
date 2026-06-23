package httpserver

import (
	"net/http"

	"github.com/justtrackio/gosoline/pkg/encoding/json"
)

// Response is the typed response returned by bound HTTP handlers.
type Response interface {
	ContentType() string
	Body() ([]byte, error)
	Header() http.Header
	StatusCode() int
}

var _ Response = &response{}

type response struct {
	body       []byte
	header     http.Header
	statusCode int
}

// NewResponse creates a response with an empty body and status 200 by default.
func NewResponse(options ...ResponseOption) *response {
	resp := &response{
		body:       []byte{},
		header:     make(http.Header),
		statusCode: http.StatusOK,
	}

	for _, option := range options {
		option(resp)
	}

	return resp
}

func (r response) ContentType() string {
	if r.header == nil {
		return ""
	}

	return r.header.Get("Content-Type")
}

func (r response) Body() ([]byte, error) {
	return r.body, nil
}

func (r response) Header() http.Header {
	return r.header
}

func (r response) StatusCode() int {
	return r.statusCode
}

// NewStatusResponse creates a response with an HTTP status code. For client or
// server error responses, it includes the standard status text as plain-text body.
func NewStatusResponse(statusCode int, options ...ResponseOption) *response {
	responseOptions := append([]ResponseOption{WithStatusCode(statusCode)}, options...)

	if statusCode >= http.StatusBadRequest {
		responseOptions = append([]ResponseOption{
			WithBody([]byte(http.StatusText(statusCode))),
			WithHeader(HeaderContentType, ContentTypeTextPlain),
		}, responseOptions...)
	}

	return NewResponse(responseOptions...)
}

// NewTextResponse creates a plain-text response with status 200 by default.
func NewTextResponse(text string, options ...ResponseOption) *response {
	responseOptions := append([]ResponseOption{
		WithBody([]byte(text)),
		WithHeader(HeaderContentType, ContentTypeTextPlain),
		WithStatusCode(http.StatusOK),
	}, options...)

	return NewResponse(responseOptions...)
}

var _ Response = &jsonResponse[string]{}

type jsonResponse[T any] struct {
	*response
	body T
}

// NewJsonResponse creates a JSON response with status 200 by default.
func NewJsonResponse[T any](body T, options ...ResponseOption) *jsonResponse[T] {
	header := make(http.Header)
	header.Set(HeaderContentType, ContentTypeJson)

	resp := &jsonResponse[T]{
		response: &response{
			header:     header,
			statusCode: http.StatusOK,
		},
		body: body,
	}

	for _, option := range options {
		option(resp.response)
	}

	return resp
}

func (j jsonResponse[T]) Body() ([]byte, error) {
	return json.Marshal(j.body)
}

func (j jsonResponse[T]) Header() http.Header {
	return j.header
}

func (j jsonResponse[T]) StatusCode() int {
	return j.statusCode
}
