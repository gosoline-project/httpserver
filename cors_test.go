package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCorsConfig(pattern string) cfg.Config {
	return cfg.New(map[string]any{
		"httpserver": map[string]any{
			"default": map[string]any{
				"cors": map[string]any{
					"allowed_origin_pattern": pattern,
					"allowed_headers":        []string{httpserver.HeaderContentType},
					"allowed_methods":        []string{"GET", "POST"},
				},
			},
		},
	})
}

func newCorsRouter(t *testing.T, pattern string) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	handler, err := httpserver.Cors(newCorsConfig(pattern), "default")
	require.NoError(t, err)

	router := gin.New()
	router.Use(handler)
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	return router
}

func serveCorsPreflight(t *testing.T, router *gin.Engine, origin string) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequest(http.MethodOptions, "/", http.NoBody)
	require.NoError(t, err)
	req.Header.Set(httpserver.HeaderOrigin, origin)
	req.Header.Set(httpserver.HeaderAccessControlRequestMethod, "GET")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

func TestCors_AnchoredPattern_PreventsPartialMatch(t *testing.T) {
	router := newCorsRouter(t, `https://example\.com`)
	rec := serveCorsPreflight(t, router, "https://example.com.evil.com")

	assert.Empty(t, rec.Header().Get(httpserver.HeaderAccessControlAllowOrigin))
}

func TestCors_AnchoredPattern_AllowsExactMatch(t *testing.T) {
	router := newCorsRouter(t, `https://example\.com`)
	rec := serveCorsPreflight(t, router, "https://example.com")

	assert.Equal(t, "https://example.com", rec.Header().Get(httpserver.HeaderAccessControlAllowOrigin))
}

func TestCors_AnchoredPattern_PreventsPrefixBypass(t *testing.T) {
	router := newCorsRouter(t, `https://example\.com`)
	rec := serveCorsPreflight(t, router, "evil.https://example.com")

	assert.Empty(t, rec.Header().Get(httpserver.HeaderAccessControlAllowOrigin))
}
