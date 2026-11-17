package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/require"
)

type bindingWithCause struct {
	binding.Binding
	cause error
}

func (b bindingWithCause) Bind(request *http.Request, value any) error {
	err := b.Binding.Bind(request, value)
	if err != nil {
		return errors.Join(err, b.cause)
	}

	return nil
}

func TestBindAdaptersReportBindingError(t *testing.T) {
	cause := errors.New("binding cause")
	cases := []struct {
		name   string
		route  gin.HandlerFunc
		method string
	}{
		{
			name: "ordinary",
			route: httpserver.BindR(func(context.Context, *http.Request, *bindJsonInput) (httpserver.Response, error) {
				return nil, nil
			}, bindingWithCause{Binding: binding.JSON, cause: cause}),
			method: http.MethodPost,
		},
		{
			name: "SSE",
			route: httpserver.BindSseR(func(context.Context, *http.Request, *bindJsonInput, *httpserver.SseWriter) error {
				return nil
			}, bindingWithCause{Binding: binding.JSON, cause: cause}),
			method: http.MethodPost,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			router := gin.New()
			var recordedError *gin.Error
			router.Use(func(c *gin.Context) {
				c.Next()
				if len(c.Errors) > 0 {
					recordedError = c.Errors.Last()
				}
			})
			router.Handle(testCase.method, "/bind", testCase.route)

			request := httptest.NewRequest(testCase.method, "/bind", strings.NewReader(`{"name":`))
			request.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			require.NotNil(t, recordedError)
			require.True(t, recordedError.IsType(gin.ErrorTypeBind))
			var bindingErr *httpserver.BindingError
			require.ErrorAs(t, recordedError.Err, &bindingErr)
			require.ErrorIs(t, recordedError.Err, cause)
			require.Equal(t, bindingErr.Err.Error(), bindingErr.Error())
			require.ErrorAs(t, fmt.Errorf("request context: %w", recordedError.Err), &bindingErr)
		})
	}
}

func TestBindingErrorStatusRespectsExplicitStatusAndMappers(t *testing.T) {
	cause := errors.New("invalid request")
	bindingErr := fmt.Errorf("request context: %w", &httpserver.BindingError{Err: cause})
	mapper := func(err error) (int, bool) {
		if errors.Is(err, cause) {
			return http.StatusForbidden, true
		}

		return 0, false
	}

	require.Equal(t, http.StatusBadRequest, httpserver.GetErrorStatusCode(bindingErr))
	require.Equal(t, http.StatusForbidden, httpserver.GetErrorStatusCodeWithMappers(bindingErr, mapper))
	require.Equal(t, http.StatusTeapot, httpserver.GetErrorStatusCodeWithMappers(httpserver.NewErrorWithStatus(http.StatusTeapot, bindingErr), mapper))
}
