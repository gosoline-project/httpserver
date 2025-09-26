package httpserver

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

type sample struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestResponseCases(t *testing.T) {
	cases := []struct {
		name          string
		build         func() Response
		expectBody    string
		expectStatus  int
		expectHeaders map[string]string
	}{
		{
			name: "default",
			build: func() Response { return NewResponse() },
			expectBody:   "",
			expectStatus: http.StatusOK,
			expectHeaders: map[string]string{},
		},
		{
			name: "options",
			build: func() Response { return NewResponse(WithBody([]byte("hello")), WithHeader("X-Test", "abc"), WithStatusCode(http.StatusAccepted)) },
			expectBody:   "hello",
			expectStatus: http.StatusAccepted,
			expectHeaders: map[string]string{"X-Test": "abc"},
		},
		{
			name: "status response",
			build: func() Response { return NewStatusResponse(http.StatusNotFound) },
			expectBody:   "",
			expectStatus: http.StatusNotFound,
			expectHeaders: map[string]string{},
		},
		{
			name: "text response",
			build: func() Response { return NewTextResponse("plain") },
			expectBody:   "plain",
			expectStatus: http.StatusOK,
			expectHeaders: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		},
		{
			name: "json response",
			build: func() Response { return NewJsonResponse(sample{Name: "alice", Age: 30}) },
			expectBody:   `{"name":"alice","age":30}`,
			expectStatus: http.StatusOK,
			expectHeaders: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		},
		{
			name: "json response with options",
			build: func() Response { return NewJsonResponse(sample{Name: "bob", Age: 21}, WithStatusCode(http.StatusCreated), WithHeader("X-App", "test")) },
			expectBody:   `{"name":"bob","age":21}`,
			expectStatus: http.StatusCreated,
			expectHeaders: map[string]string{"X-App": "test"},
		},
	}

	for _, tc := range cases {
			resp := tc.build()

			b, err := resp.Body()
			assert.NoError(t, err)

			if tc.expectBody == "" {
				assert.Equal(t, "", string(b))
			} else if resp.Header().Get("Content-Type") == "application/json; charset=utf-8" {
				assert.JSONEq(t, tc.expectBody, string(b))
			} else {
				assert.Equal(t, tc.expectBody, string(b))
			}
			assert.Equal(t, tc.expectStatus, resp.StatusCode())
			for k, v := range tc.expectHeaders {
				assert.Equal(t, v, resp.Header().Get(k))
			}
			if len(tc.expectHeaders) == 0 {
				assert.Empty(t, resp.Header())
			}
	}
}
