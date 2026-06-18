package httpserver

import (
	"net/http"

	"github.com/justtrackio/gosoline/pkg/encoding/json"
)

// Response is the typed response returned by bound HTTP handlers.
type Response interface {
	Body() ([]byte, error)
	Header() http.Header
	StatusCode() int
}

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

func (j response) Body() ([]byte, error) {
	return j.body, nil
}

func (j response) Header() http.Header {
	return j.header
}

func (j response) StatusCode() int {
	return j.statusCode
}

// NewStatusResponse creates a response with only an HTTP status code.
func NewStatusResponse(statusCode int, options ...ResponseOption) *response {
	responseOptions := append([]ResponseOption{WithStatusCode(statusCode)}, options...)

	return NewResponse(responseOptions...)
}

// NewTextResponse creates a plain-text response with status 200 by default.
func NewTextResponse(text string, options ...ResponseOption) *response {
	responseOptions := append([]ResponseOption{
		WithBody([]byte(text)),
		WithHeader("Content-Type", "text/plain; charset=utf-8"),
		WithStatusCode(http.StatusOK),
	}, options...)

	return NewResponse(responseOptions...)
}

type jsonResponse[T any] struct {
	*response
	body T
}

// NewJsonResponse creates a JSON response with status 200 by default.
func NewJsonResponse[T any](body T, options ...ResponseOption) *jsonResponse[T] {
	header := make(http.Header)
	header.Set("Content-Type", "application/json; charset=utf-8")

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
