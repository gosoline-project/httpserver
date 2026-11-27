package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestRouteTracingMiddleware(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))

	router := gin.New()
	router.Use(httpserver.RouteTracingMiddleware)
	router.POST("/v1/users/:id", func(ginCtx *gin.Context) {
		ginCtx.Status(http.StatusOK)
	})

	handler := otelhttp.NewHandler(router, "server", otelhttp.WithTracerProvider(tracerProvider))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/users/42", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/unknown/path", http.NoBody))

	spans := exporter.GetSpans()
	if !assert.Len(t, spans, 2) {
		return
	}

	assert.Equal(t, "POST /v1/users/:id", spans[0].Name)
	assert.Contains(t, spans[0].Attributes, semconv.HTTPRoute("/v1/users/:id"))

	assert.Equal(t, "POST", spans[1].Name)
	for _, attribute := range spans[1].Attributes {
		assert.NotEqual(t, semconv.HTTPRouteKey, attribute.Key)
	}
}
