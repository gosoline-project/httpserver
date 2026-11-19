package httpserver_test

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/embedded_static
var embeddedStaticFiles embed.FS

func embeddedStaticRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	middleware, err := httpserver.CreateEmbeddedStaticServe(embeddedStaticFiles, "testdata/embedded_static", "/api")(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	router := gin.New()
	router.Use(middleware)
	router.GET("/api/ping", func(c *gin.Context) { c.String(http.StatusOK, "api") })

	return router
}

func TestEmbeddedStaticServe(t *testing.T) {
	router := embeddedStaticRouter(t)
	for _, testCase := range []struct {
		path        string
		status      int
		body        string
		contentType string
	}{
		{"/", http.StatusOK, "<!doctype html><html><body>Dashboard</body></html>\n", "text/html"},
		{"/settings/profile", http.StatusOK, "<!doctype html><html><body>Dashboard</body></html>\n", "text/html"},
		{"/index.html", http.StatusOK, "<!doctype html><html><body>Dashboard</body></html>\n", "text/html"},
		{"/app.js?v=1", http.StatusOK, "console.log(\"embedded asset\");\n", "javascript"},
		{"/missing.js", http.StatusNotFound, "", ""},
		{"/api/ping", http.StatusOK, "api", "text/plain"},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, testCase.path, http.NoBody))
			assert.Equal(t, testCase.status, response.Code)

			if testCase.body != "" {
				assert.Equal(t, testCase.body, response.Body.String())
			}

			assert.Contains(t, response.Header().Get("Content-Type"), testCase.contentType)
		})
	}
}
