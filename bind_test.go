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
	"github.com/gosoline-project/httpserver/mocks"
	"github.com/stretchr/testify/assert"
)

type bindJsonInput struct {
	Name string `json:"name"`
}

type bindValidatedJsonInput struct {
	Name  string `json:"name" binding:"required"`
	Count int    `json:"count" binding:"required"`
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
			// JSON requests should bind from the body when the content type is application/json.
			name: "json success",
			register: func(r *gin.Engine) {
				r.POST("/json", httpserver.Bind(func(ctx context.Context, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/json",
			body:         `{"name":"alice"}`,
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeApplicationJson},
			expectedCode: http.StatusOK,
			expectedBody: `{"name":"alice"}`,
		},
		{
			// URI parameters should bind after request body binding and populate uri-tagged fields.
			name: "uri success",
			register: func(r *gin.Engine) {
				r.GET("/obj/:id", httpserver.Bind(func(ctx context.Context, input *bindUriInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/obj/7",
			body:         `{}`,
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeApplicationJson},
			expectedCode: http.StatusOK,
			expectedBody: `{"id":7}`,
		},
		{
			// Body and URI binding should compose into the same input value.
			name: "json and uri success",
			register: func(r *gin.Engine) {
				r.POST("/mixed/:id", httpserver.Bind(func(ctx context.Context, input *bindMixedInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/mixed/3",
			body:         `{"name":"bob"}`,
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeApplicationJson},
			expectedCode: http.StatusOK,
			expectedBody: `{"id":3,"name":"bob"}`,
		},
		{
			// BindR should pass the raw request to handlers that need request metadata.
			name: "bindR request propagation",
			register: func(r *gin.Engine) {
				r.POST("/r", httpserver.BindR(func(ctx context.Context, req *http.Request, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(map[string]string{"method": req.Method}), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/r",
			body:         `{"name":"alice"}`,
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeApplicationJson},
			expectedCode: http.StatusOK,
			expectedBody: `{"method":"POST"}`,
		},
		{
			// BindN should support successful handlers that intentionally return no body.
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
			// 304 responses must not attempt to serialize or write a response body.
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
			// HEAD responses should keep the status and headers but suppress the response body.
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
			// Query tags should bind from URL query parameters without requiring a request body.
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
			// Header tags should bind from request headers alongside body binding.
			name: "header success",
			register: func(r *gin.Engine) {
				r.GET("/header", httpserver.Bind(func(ctx context.Context, input *bindHeaderInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodGet,
			path:         "/header",
			body:         `{}`,
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeApplicationJson, httpserver.HeaderAuthorization: "Bearer token"},
			expectedCode: http.StatusOK,
			expectedBody: `{"auth":"Bearer token"}`,
		},
		{
			// Form content type should bind form fields from an urlencoded body.
			name: "form success",
			register: func(r *gin.Engine) {
				r.POST("/form", httpserver.Bind(func(ctx context.Context, input *bindFormInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/form",
			body:         "name=alice&email=alice%40example.com",
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeFormURLEncoded},
			expectedCode: http.StatusOK,
			expectedBody: `{"Name":"alice","Email":"alice@example.com"}`,
		},
		{
			// Form-tagged inputs should combine urlencoded body fields with query parameters.
			name: "query + form both success",
			register: func(r *gin.Engine) {
				r.POST("/searchform", httpserver.Bind(func(ctx context.Context, input *bindQueryInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			method:       http.MethodPost,
			path:         "/searchform?search=golang",
			body:         "page=2",
			headers:      map[string]string{httpserver.HeaderContentType: httpserver.ContentTypeFormURLEncoded},
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

func TestBindFailureCases(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		expectedBody string
	}{
		{
			// Invalid JSON should be reported as a binding error with the binder name for context.
			name:         "json invalid",
			body:         `{"name":`,
			expectedBody: `{"err":"json: unexpected EOF"}`,
		},
		{
			// Validator errors come from the bind phase, but should not be prefixed with the binder name.
			name:         "validation error returns bad request",
			body:         `{"count":1}`,
			expectedBody: `{"err":"Key: 'bindValidatedJsonInput.Name' Error:Field validation for 'Name' failed on the 'required' tag"}`,
		},
		{
			// Decode errors should short-circuit validation and avoid duplicate prefixes like "json: json:".
			name:         "bind error takes precedence over validation error",
			body:         `{"count":"not-a-number"}`,
			expectedBody: `{"err":"json: cannot unmarshal string into Go struct field bindValidatedJsonInput.count of type int"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlerCalled := false
			r := newTestRouter(func(r *gin.Engine) {
				r.POST("/validated", httpserver.Bind(func(ctx context.Context, input *bindValidatedJsonInput) (httpserver.Response, error) {
					handlerCalled = true

					return httpserver.NewJsonResponse(input), nil
				}))
			})

			req := httptest.NewRequest(http.MethodPost, "/validated", strings.NewReader(tc.body))
			req.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)
			recorder := httptest.NewRecorder()

			r.ServeHTTP(recorder, req)

			assert.False(t, handlerCalled)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.JSONEq(t, tc.expectedBody, recorder.Body.String())
		})
	}
}

func TestBindReportsBindErrorType(t *testing.T) {
	cases := []struct {
		name     string
		register func(r *gin.Engine)
		body     string
	}{
		{
			// Binding failures should be marked as Gin bind errors for middleware/logging.
			name: "bind error",
			register: func(r *gin.Engine) {
				r.POST("/json", httpserver.Bind(func(ctx context.Context, input *bindJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			body: `{"name":`,
		},
		{
			// Validation failures during binding should also be marked as Gin bind errors.
			name: "validation error",
			register: func(r *gin.Engine) {
				r.POST("/json", httpserver.Bind(func(ctx context.Context, input *bindValidatedJsonInput) (httpserver.Response, error) {
					return httpserver.NewJsonResponse(input), nil
				}))
			},
			body: `{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			r := gin.New()
			r.Use(func(ctx *gin.Context) {
				ctx.Next()

				assert.Len(t, ctx.Errors, 1)
				assert.True(t, ctx.Errors[0].IsType(gin.ErrorTypeBind))
			})
			tc.register(r)

			req := httptest.NewRequest(http.MethodPost, "/json", strings.NewReader(tc.body))
			req.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)
			recorder := httptest.NewRecorder()

			r.ServeHTTP(recorder, req)
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
			// 204 responses are bodyless, so Body and Writer.Write must not be called.
			name:       "204 no content",
			method:     http.MethodGet,
			statusCode: http.StatusNoContent,
		},
		{
			// 304 responses are bodyless, so Body and Writer.Write must not be called.
			name:       "304 not modified",
			method:     http.MethodGet,
			statusCode: http.StatusNotModified,
		},
		{
			// HEAD requests are bodyless regardless of status, so Body and Writer.Write must not be called.
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

			response := mocks.NewResponse(t)
			response.EXPECT().StatusCode().Return(tc.statusCode).Once()
			response.EXPECT().Header().Return(http.Header{"X-Test": []string{"set"}}).Once()

			err := httpserver.BindHandleResponse(response, ginCtx)
			assert.NoError(t, err)
			response.AssertNotCalled(t, "Body")
			assert.Equal(t, 0, writer.writeCalls)
			assert.Equal(t, tc.statusCode, recorder.Code)
			assert.Empty(t, recorder.Body.String())
			assert.Equal(t, "set", recorder.Header().Get("X-Test"))
		})
	}
}
