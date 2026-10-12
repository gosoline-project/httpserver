package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/justtrackio/gosoline/pkg/metric"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetricMiddlewareEmitsCanonicalOtelMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(t.Context()))
	})

	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	otelWriter := metric.NewOtelWriterWithInterfaces(logger, provider.Meter("httpserver-test"))
	writer := namespacedMetricWriter{namespace: metricNamespace, writer: otelWriter}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		MetricMiddleware("api", c, writer, noopMetricRecorder{})
	})
	router.Use(func(c *gin.Context) {
		time.Sleep(20 * time.Millisecond)
		c.Request = MarkRequestRejected(c.Request)
		c.AbortWithStatus(http.StatusTooManyRequests)
	})
	router.GET("/widgets/:id", func(c *gin.Context) {})

	request := httptest.NewRequest(http.MethodGet, "/widgets/42", http.NoBody)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusTooManyRequests, response.Code)

	var exported metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &exported))

	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range exported.ScopeMetrics {
		for _, exportedMetric := range scope.Metrics {
			metrics[exportedMetric.Name] = exportedMetric
		}
	}
	require.Len(t, metrics, 2)

	duration, ok := metrics["gosoline.http.server.request.duration"]
	require.True(t, ok)
	require.Equal(t, "s", duration.Unit)
	durationHistogram, ok := duration.Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, durationHistogram.DataPoints, 1)
	durationPoint := durationHistogram.DataPoints[0]
	require.Equal(t, uint64(1), durationPoint.Count)
	require.Greater(t, durationPoint.Sum, 0.01)
	require.Less(t, durationPoint.Sum, 1.0)
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("name", "api"),
		attribute.String("route", "/widgets/:id"),
		attribute.String("request.method", http.MethodGet),
		attribute.String("response.status_code", "429"),
	}, durationPoint.Attributes.ToSlice())

	rejected, ok := metrics["gosoline.http.server.rejected.requests"]
	require.True(t, ok)
	require.Equal(t, "{request}", rejected.Unit)
	rejectedCounter, ok := rejected.Data.(metricdata.Sum[float64])
	require.True(t, ok)
	require.Len(t, rejectedCounter.DataPoints, 1)
	require.Equal(t, float64(1), rejectedCounter.DataPoints[0].Value)
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("name", "api"),
		attribute.String("route", "/widgets/:id"),
		attribute.String("request.method", http.MethodGet),
	}, rejectedCounter.DataPoints[0].Attributes.ToSlice())
}

type namespacedMetricWriter struct {
	namespace string
	writer    metric.Writer
}

func (w namespacedMetricWriter) GetPriority() int {
	return w.writer.GetPriority()
}

func (w namespacedMetricWriter) Write(ctx context.Context, batch metric.Data) {
	for _, datum := range batch {
		if datum.Namespace == "" {
			datum.Namespace = w.namespace
		}
	}

	w.writer.Write(ctx, batch)
}

func (w namespacedMetricWriter) WriteOne(ctx context.Context, datum *metric.Datum) {
	w.Write(ctx, metric.Data{datum})
}

type noopMetricRecorder struct{}

func (noopMetricRecorder) TrackRequestStarted(context.Context)   {}
func (noopMetricRecorder) TrackRequestCompleted(context.Context) {}
func (noopMetricRecorder) TrackConnectionOpened(context.Context) {}
func (noopMetricRecorder) TrackConnectionClosed(context.Context) {}
func (noopMetricRecorder) Run(context.Context) error             { return nil }
