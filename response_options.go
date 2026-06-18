package httpserver

import "net/http"

// ResponseOption modifies a response while it is being created.
type ResponseOption func(*response)

// WithBody sets the raw response body.
func WithBody(body []byte) ResponseOption {
	return func(r *response) {
		r.body = body
	}
}

// WithHeader adds one header value to the response.
func WithHeader(key string, value string) ResponseOption {
	return func(r *response) {
		r.header.Add(key, value)
	}
}

// WithHeaders adds all provided header values to the response.
func WithHeaders(headers http.Header) ResponseOption {
	return func(r *response) {
		for k, v := range headers {
			for _, vv := range v {
				r.header.Add(k, vv)
			}
		}
	}
}

// WithStatusCode sets the response status code.
func WithStatusCode(statusCode int) ResponseOption {
	return func(r *response) {
		r.statusCode = statusCode
	}
}
