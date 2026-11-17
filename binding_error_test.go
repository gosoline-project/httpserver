package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/require"
)

func TestBindAdaptersReportBindingError(t *testing.T) {
	handlerCalled := false
	cases := []struct {
		name  string
		route gin.HandlerFunc
	}{
		{
			name: "ordinary",
			route: httpserver.BindR(func(context.Context, *http.Request, *bindJsonInput) (httpserver.Response, error) {
				handlerCalled = true

				return nil, nil
			}),
		},
		{
			name: "sse",
			route: httpserver.BindSseR(func(context.Context, *http.Request, *bindJsonInput, *httpserver.SseWriter) error {
				handlerCalled = true

				return nil
			}),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handlerCalled = false

			router := gin.New()
			var recordedError *gin.Error
			router.Use(func(c *gin.Context) {
				c.Next()
				if len(c.Errors) > 0 {
					recordedError = c.Errors.Last()
				}
			})
			router.POST("/bind", testCase.route)

			request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":`))
			request.Header.Set(httpserver.HeaderContentType, httpserver.ContentTypeApplicationJson)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			require.NotNil(t, recordedError)
			require.False(t, handlerCalled)
			require.True(t, recordedError.IsType(gin.ErrorTypeBind))
			var bindingErr *httpserver.BindingError
			require.ErrorAs(t, recordedError.Err, &bindingErr)
			require.ErrorIs(t, recordedError.Err, io.ErrUnexpectedEOF)
			require.ErrorAs(t, fmt.Errorf("request context: %w", recordedError.Err), &bindingErr)
		})
	}
}

func TestBindingErrorStatusRespectsExplicitStatusAndMappers(t *testing.T) {
	cause := errors.New("invalid request")
	bindingErr := fmt.Errorf("request context: %w", &httpserver.BindingError{
		Err: cause,
	})
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
