package httpserver

import (
	"github.com/gin-gonic/gin"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// RouteTracingMiddleware names the server span "<method> <route>" and sets http.route from the matched gin route template.
// Requests that match no route keep the plain method name, which keeps span names bounded.
func RouteTracingMiddleware(ginCtx *gin.Context) {
	if route := ginCtx.FullPath(); route != "" {
		span := trace.SpanFromContext(ginCtx.Request.Context())
		span.SetName(ginCtx.Request.Method + " " + route)
		span.SetAttributes(semconv.HTTPRoute(route))
	}

	ginCtx.Next()
}
