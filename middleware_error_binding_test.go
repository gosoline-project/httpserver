package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/require"
)

func TestBadRequestPrivacy(t *testing.T) {
	cases := []struct {
		name       string
		privacy    string
		wantBody   string
		wantHeader string
		wantRetry  string
		wantMulti  []string
	}{
		{
			name:     "default",
			wantBody: `{"err":"bad request"}`,
		},
		{
			name:     "private",
			privacy:  httpserver.ErrorPrivacyPrivate,
			wantBody: `{"err":"bad request"}`,
		},
		{
			name:       "public",
			privacy:    httpserver.ErrorPrivacyPublic,
			wantBody:   `{"err":"error with headers"}`,
			wantHeader: "custom",
			wantRetry:  "30",
			wantMulti:  []string{"first", "second"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(httpserver.ErrorMiddlewareWithSettings(httpserver.ErrorsSettings{
				Privacy: testCase.privacy,
			}))
			router.GET("/error", func(c *gin.Context) {
				require.NotNil(t, c.Error(errorWithHeaders{
					statusCode: http.StatusBadRequest,
				}))
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/error", http.NoBody)
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.JSONEq(t, testCase.wantBody, recorder.Body.String())
			require.Equal(t, testCase.wantHeader, recorder.Header().Get("X-Error"))
			require.Equal(t, testCase.wantRetry, recorder.Header().Get(httpserver.HeaderRetryAfter))
			require.Equal(t, testCase.wantMulti, recorder.Header().Values("X-Multi-Value"))
		})
	}
}
