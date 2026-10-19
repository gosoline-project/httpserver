package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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
	var observedCounters []metric.Datum
	var observedCountersMutex sync.Mutex
	writer := namespacedMetricWriter{
		namespace: metricNamespace,
		writer:    otelWriter,
		observe: func(batch metric.Data) {
			observedCountersMutex.Lock()
			defer observedCountersMutex.Unlock()
			for _, datum := range batch {
				switch datum.MetricName {
				case "requests", "routed.requests", MetricHttpRequestsRejected:
					observedCounters = append(observedCounters, *datum)
				}
			}
		},
	}
	definitions := []Definition{
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/widgets/:id"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/auth"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/chaos"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/idle"},
	}
	writer.Write(t.Context(), GetMetricMiddlewareDefaults("api", definitions...))

	router := gin.New()
	// Match NewServerWithSettings ordering: metrics wrap the concurrency and chaos gates.
	router.Use(func(c *gin.Context) {
		MetricMiddleware("api", c, writer, noopMetricRecorder{})
	})
	// Production registers health after metric middleware but before overload and chaos middleware.
	router.GET("/health", func(c *gin.Context) {})
	router.Use(ConcurrentRequestLimitMiddleware(ConcurrencySettings{
		MaxRequests:        1,
		OverloadStatusCode: http.StatusTooManyRequests,
	}))

	router.Use(ChaosMiddleware(t.Context(), logger, ChaosSettings{}))
	handlerStarted := make(chan struct{}, 2)
	releaseHandler := make(chan struct{}, 2)
	defer func() {
		releaseHandler <- struct{}{}
		releaseHandler <- struct{}{}
	}()
	router.GET("/widgets/:id", func(c *gin.Context) {
		time.Sleep(20 * time.Millisecond)
		handlerStarted <- struct{}{}
		<-releaseHandler
	})
	router.GET("/auth", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	}, func(c *gin.Context) {})
	router.GET("/chaos", ChaosMiddleware(t.Context(), logger, ChaosSettings{
		Enabled: true,
		Reject: ChaosRejectSettings{
			Percent:     100,
			StatusCodes: []int{http.StatusServiceUnavailable},
		},
	}), func(c *gin.Context) {})
	router.GET("/idle", func(c *gin.Context) {})

	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		router.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "/widgets/42", http.NoBody))
		close(firstDone)
	}()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not enter its handler")
	}

	overloadResponse := httptest.NewRecorder()
	router.ServeHTTP(overloadResponse, httptest.NewRequest(http.MethodGet, "/widgets/42", http.NoBody))
	require.Equal(t, http.StatusTooManyRequests, overloadResponse.Code)
	releaseHandler <- struct{}{}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
	require.Equal(t, http.StatusOK, firstResponse.Code)

	authResponse := httptest.NewRecorder()
	router.ServeHTTP(authResponse, httptest.NewRequest(http.MethodGet, "/auth", http.NoBody))
	require.Equal(t, http.StatusUnauthorized, authResponse.Code)
	chaosResponse := httptest.NewRecorder()
	router.ServeHTTP(chaosResponse, httptest.NewRequest(http.MethodGet, "/chaos", http.NoBody))
	require.Equal(t, http.StatusServiceUnavailable, chaosResponse.Code)
	healthResponse := httptest.NewRecorder()
	router.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	require.Equal(t, http.StatusOK, healthResponse.Code)
	unmatchedResponse := httptest.NewRecorder()
	router.ServeHTTP(unmatchedResponse, httptest.NewRequest(http.MethodGet, "/missing", http.NoBody))
	require.Equal(t, http.StatusNotFound, unmatchedResponse.Code)

	var exported metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &exported))

	observedCountersMutex.Lock()
	counterSamples := append([]metric.Datum(nil), observedCounters...)
	observedCountersMutex.Unlock()
	for _, datum := range counterSamples {
		require.Equal(t, metric.PriorityHigh, datum.Priority)
		require.Equal(t, metric.UnitCount, datum.Unit)
		require.Equal(t, metric.KindCounter.Build(), datum.Kind)
	}

	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range exported.ScopeMetrics {
		for _, exportedMetric := range scope.Metrics {
			metrics[exportedMetric.Name] = exportedMetric
		}
	}
	require.Len(t, metrics, 4)

	duration, ok := metrics["gosoline.http.server.request.duration"]
	require.True(t, ok)
	require.Equal(t, "s", duration.Unit)
	durationHistogram, ok := duration.Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, durationHistogram.DataPoints, 5)
	statuses := make(map[string]bool, 5)
	for _, point := range durationHistogram.DataPoints {
		attributes := point.Attributes.ToSlice()
		route := metricStringAttribute(attributes, "route")
		status := metricStringAttribute(attributes, "response.status_code")
		require.Equal(t, uint64(1), point.Count)
		require.Greater(t, point.Sum, 0.0)
		switch route {
		case "/widgets/:id":
			require.Contains(t, []string{"200", "429"}, status)
			if status == "200" {
				require.Greater(t, point.Sum, 0.01)
			}
		case "/auth":
			require.Equal(t, "401", status)
		case "/chaos":
			require.Equal(t, "503", status)
		case "/health":
			require.Equal(t, "200", status)
		default:
			require.FailNow(t, "unexpected duration route", route)
		}
		require.ElementsMatch(t, []attribute.KeyValue{
			attribute.String("name", "api"),
			attribute.String("route", route),
			attribute.String("request.method", http.MethodGet),
			attribute.String("response.status_code", status),
		}, attributes)
		statuses[route+":"+status] = true
	}
	require.Equal(t, map[string]bool{
		"/widgets/:id:200": true,
		"/widgets/:id:429": true,
		"/auth:401":        true,
		"/chaos:503":       true,
		"/health:200":      true,
	}, statuses)

	requests, ok := metrics["gosoline.http.server.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, requests, map[string]float64{"": 5}, false)

	routedRequests, ok := metrics["gosoline.http.server.routed.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, routedRequests, map[string]float64{
		"/widgets/:id": 2,
		"/auth":        1,
		"/chaos":       1,
		"/idle":        0,
		"/health":      1,
	}, true)

	rejected, ok := metrics["gosoline.http.server.rejected.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, rejected, map[string]float64{
		"/widgets/:id": 1,
		"/auth":        0,
		"/chaos":       0,
		"/idle":        0,
	}, true)
}

func assertOtelCounterSeries(t *testing.T, exported metricdata.Metrics, expectedValues map[string]float64, perRoute bool) {
	t.Helper()

	require.Equal(t, "{request}", exported.Unit)
	counter, ok := exported.Data.(metricdata.Sum[float64])
	require.True(t, ok)
	require.True(t, counter.IsMonotonic)
	require.Len(t, counter.DataPoints, len(expectedValues))

	actualValues := make(map[string]float64, len(counter.DataPoints))
	for _, point := range counter.DataPoints {
		attributes := point.Attributes.ToSlice()
		route := metricStringAttribute(attributes, "route")
		if perRoute {
			require.ElementsMatch(t, []attribute.KeyValue{
				attribute.String("name", "api"),
				attribute.String("route", route),
				attribute.String("request.method", http.MethodGet),
			}, attributes)
		} else {
			require.ElementsMatch(t, []attribute.KeyValue{attribute.String("name", "api")}, attributes)
		}
		actualValues[route] = point.Value
	}
	require.Equal(t, expectedValues, actualValues)
}

func metricStringAttribute(attributes []attribute.KeyValue, name string) string {
	for _, keyValue := range attributes {
		if string(keyValue.Key) == name {
			return keyValue.Value.AsString()
		}
	}

	return ""
}

type namespacedMetricWriter struct {
	namespace string
	writer    metric.Writer
	observe   func(metric.Data)
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
	if w.observe != nil {
		w.observe(batch)
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
