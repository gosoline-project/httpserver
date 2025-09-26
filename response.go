package httpserver

import (
	"net/http"

	"github.com/justtrackio/gosoline/pkg/encoding/json"
)

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

func NewStatusResponse(statusCode int, options ...ResponseOption) *response {
	return NewResponse(WithStatusCode(statusCode))
}

func NewTextResponse(text string, options ...ResponseOption) *response {
	return NewResponse(
		WithBody([]byte(text)),
		WithHeader("Content-Type", "text/plain; charset=utf-8"),
		WithStatusCode(http.StatusOK),
	)
}

type jsonResponse[T any] struct {
	*response
	body T
}

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
