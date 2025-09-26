package httpserver

import "net/http"

type ResponseOption func(*response)

func WithBody(body []byte) ResponseOption {
	return func(r *response) {
		r.body = body
	}
}

func WithHeader(key string, value string) ResponseOption {
	return func(r *response) {
		r.header.Add(key, value)
	}
}

func WithHeaders(headers http.Header) ResponseOption {
	return func(r *response) {
		for k, v := range headers {
			for _, vv := range v {
				r.header.Add(k, vv)
			}
		}
	}
}

func WithStatusCode(statusCode int) ResponseOption {
	return func(r *response) {
		r.statusCode = statusCode
	}
}
