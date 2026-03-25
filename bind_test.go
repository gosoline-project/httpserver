package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
)

type bindJsonInput struct {
	Name string `json:"name"`
}
type bindUriInput struct {
	Id int `uri:"id" json:"id"`
}
type bindMixedInput struct {
	Id   int    `uri:"id" json:"id"`
	Name string `json:"name"`
}

type bindQueryInput struct {
	Search string `form:"search"`
	Page   int    `form:"page"`
}

type bindHeaderInput struct {
	Auth string `header:"Authorization" json:"auth"`
}

type bindFormInput struct {
	Name  string `form:"name"`
	Email string `form:"email"`
}

type trackingResponse struct {
	statusCode int
	header     http.Header
	body       []byte
	bodyCalled bool
	bodyErr    error
}

func (r *trackingResponse) Body() ([]byte, error) {
	r.bodyCalled = true

	if r.bodyErr != nil {
		return nil, r.bodyErr
	}

	return r.body, nil
}

func (r *trackingResponse) Header() http.Header {
	return r.header
}

func (r *trackingResponse) StatusCode() int {
	return r.statusCode
}

type failOnWriteResponseWriter struct {
	gin.ResponseWriter
	writeCalls int
}

func (w *failOnWriteResponseWriter) Write(p []byte) (int, error) {
	w.writeCalls++

	return 0, errors.New("write should not be called")
}

func (w *failOnWriteResponseWriter) WriteString(s string) (int, error) {
	w.writeCalls++

	return 0, errors.New("write should not be called")
}

func newTestRouter(register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(httpserver.ErrorMiddleware())

	register(r)

	return r
}

func TestBindCases(t *testing.T) {
	cases := []struct {
		name         string
		register     func(r *gin.Engine)
		method       string
		path         string
		body         string
		headers      map[string]string
		expectedCode int
		expectedBody string
	}{
		{
			name: "json success",
			register: func(r *gin.Engine) {
				r.POST("/json", httpserver.Bind(func(ctx context.Context, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/json",
			body:         `{"name":"alice"}`,
			headers:      map[string]string{"Content-Type": "application/json"},
			expectedCode: http.StatusOK,
			expectedBody: `{"name":"alice"}`,
		},
		{
			name: "json invalid",
			register: func(r *gin.Engine) {
				r.POST("/json", httpserver.Bind(func(ctx context.Context, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/json",
			body:         `{"name":`,
			headers:      map[string]string{"Content-Type": "application/json"},
			expectedCode: http.StatusInternalServerError,
			expectedBody: `{"err": "bind error: json: unexpected EOF"}`,
		},
		{
			name: "uri success",
			register: func(r *gin.Engine) {
				r.GET("/obj/:id", httpserver.Bind(func(ctx context.Context, input *bindUriInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/obj/7",
			body:         `{}`,
			headers:      map[string]string{"Content-Type": "application/json"},
			expectedCode: http.StatusOK,
			expectedBody: `{"id":7}`,
		},
		{
			name: "json and uri success",
			register: func(r *gin.Engine) {
				r.POST("/mixed/:id", httpserver.Bind(func(ctx context.Context, input *bindMixedInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/mixed/3",
			body:         `{"name":"bob"}`,
			headers:      map[string]string{"Content-Type": "application/json"},
			expectedCode: http.StatusOK,
			expectedBody: `{"id":3,"name":"bob"}`,
		},
		{
			name: "bindR request propagation",
			register: func(r *gin.Engine) {
				r.POST("/r", httpserver.BindR(func(ctx context.Context, req *http.Request, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(map[string]string{"method": req.Method}), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/r",
			body:         `{"name":"alice"}`,
			headers:      map[string]string{"Content-Type": "application/json"},
			expectedCode: http.StatusOK,
			expectedBody: `{"method":"POST"}`,
		},
		{
			name: "bindN no content",
			register: func(r *gin.Engine) {
				r.GET("/n", httpserver.BindN(func(ctx context.Context) (httpserver.Response, error) {
					return httpserver.NewStatusResponse(http.StatusNoContent), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/n",
			expectedCode: http.StatusNoContent,
			expectedBody: ``,
		},
		{
			name: "bindN not modified",
			register: func(r *gin.Engine) {
				r.GET("/not-modified", httpserver.BindN(func(ctx context.Context) (httpserver.Response, error) {
					return httpserver.NewStatusResponse(http.StatusNotModified), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/not-modified",
			expectedCode: http.StatusNotModified,
			expectedBody: ``,
		},
		{
			name: "bindN head request has no body",
			register: func(r *gin.Engine) {
				r.HEAD("/head", httpserver.BindN(func(ctx context.Context) (httpserver.Response, error) {
					return httpserver.NewTextResponse("head body"), nil
				}))
			},
			method:       http.MethodHead,
			path:         "/head",
			expectedCode: http.StatusOK,
			expectedBody: ``,
		},
		{
			name: "query success",
			register: func(r *gin.Engine) {
				r.GET("/search", httpserver.Bind(func(ctx context.Context, input *bindQueryInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/search?search=golang&page=2",
			expectedCode: http.StatusOK,
			expectedBody: `{"Search":"golang","Page":2}`,
		},
		{
			name: "header success",
			register: func(r *gin.Engine) {
				r.GET("/header", httpserver.Bind(func(ctx context.Context, input *bindHeaderInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/header",
			body:         `{}`,
			headers:      map[string]string{"Content-Type": "application/json", "Authorization": "Bearer token"},
			expectedCode: http.StatusOK,
			expectedBody: `{"auth":"Bearer token"}`,
		},
		{
			name: "form success",
			register: func(r *gin.Engine) {
				r.POST("/form", httpserver.Bind(func(ctx context.Context, input *bindFormInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/form",
			body:         "name=alice&email=alice%40example.com",
			headers:      map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
			expectedCode: http.StatusOK,
			expectedBody: `{"Name":"alice","Email":"alice@example.com"}`,
		},
		{
			name: "query + form both success",
			register: func(r *gin.Engine) {
				r.POST("/searchform", httpserver.Bind(func(ctx context.Context, input *bindQueryInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/searchform?search=golang",
			body:         "page=2",
			headers:      map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
			expectedCode: http.StatusOK,
			expectedBody: `{"Search":"golang","Page":2}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(tc.register)

			req, err := http.NewRequest(tc.method, tc.path, http.NoBody)
			assert.NoError(t, err)

			if tc.body != "" {
				req.Body = io.NopCloser(strings.NewReader(tc.body))
			}

			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, req)
			assert.Equal(t, tc.expectedCode, recorder.Code)

			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, recorder.Body.String())
			}
		})
	}
}

func TestBindHandleResponseSkipsBodyHandlingForBodylessResponses(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		statusCode int
	}{
		{
			name:       "204 no content",
			method:     http.MethodGet,
			statusCode: http.StatusNoContent,
		},
		{
			name:       "304 not modified",
			method:     http.MethodGet,
			statusCode: http.StatusNotModified,
		},
		{
			name:       "head request",
			method:     http.MethodHead,
			statusCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest(tc.method, "/bodyless", http.NoBody)

			writer := &failOnWriteResponseWriter{ResponseWriter: ginCtx.Writer}
			ginCtx.Writer = writer

			response := &trackingResponse{
				statusCode: tc.statusCode,
				header:     http.Header{"X-Test": []string{"set"}},
				body:       []byte("should not be used"),
				bodyErr:    errors.New("body should not be read"),
			}

			err := httpserver.BindHandleResponse(response, ginCtx)
			assert.NoError(t, err)
			assert.False(t, response.bodyCalled)
			assert.Equal(t, 0, writer.writeCalls)
			assert.Equal(t, tc.statusCode, recorder.Code)
			assert.Empty(t, recorder.Body.String())
			assert.Equal(t, "set", recorder.Header().Get("X-Test"))
		})
	}
}
