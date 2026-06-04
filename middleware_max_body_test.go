package httpserver_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readBodyHandler(c *gin.Context) {
	var err error
	var body []byte

	if body, err = io.ReadAll(c.Request.Body); err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"err": err.Error()})

		return
	}

	c.JSON(http.StatusOK, gin.H{"len": len(body)})
}

func newMaxBodyRouter(maxBytes int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.MaxBodySizeMiddleware(maxBytes))
	router.POST("/", readBodyHandler)

	return router
}

func TestMaxBodySizeMiddleware_SmallBodyPassesThrough(t *testing.T) {
	router := newMaxBodyRouter(100)
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"len":5`)
}

func TestMaxBodySizeMiddleware_OversizedBodyReturnsError(t *testing.T) {
	router := newMaxBodyRouter(3)
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader("hello world"))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestMaxBodySizeMiddleware_ZeroLimitDisablesEnforcement(t *testing.T) {
	router := newMaxBodyRouter(0)
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 1_000_000)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMaxBodySizeMiddleware_ExactLimitBodyPassesThrough(t *testing.T) {
	router := newMaxBodyRouter(5)
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMaxBodySizeMiddleware_GzipBodyExceedsDecompressedLimit(t *testing.T) {
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	for i := 0; i < 20_000; i++ {
		_, err := gw.Write([]byte("AAAAAAAAAABBBBBBBBBBCCCCCCCCCC"))
		require.NoError(t, err)
	}
	require.NoError(t, gw.Close())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		var err error
		var reader io.ReadCloser

		if c.GetHeader("Content-Encoding") == "gzip" {
			if reader, _, err = httpserver.NewGZipBodyReader(c.Request.Body); err != nil {
				c.AbortWithStatus(http.StatusBadRequest)

				return
			}

			c.Request.Body = reader
			c.Request.Header.Del("Content-Encoding")
		}

		c.Next()
	})
	router.Use(httpserver.MaxBodySizeMiddleware(100_000))
	router.POST("/", readBodyHandler)

	req, err := http.NewRequest(http.MethodPost, "/", &compressed)
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}
