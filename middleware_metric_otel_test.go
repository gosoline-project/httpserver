package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/clock"
	cloudwatchMocks "github.com/justtrackio/gosoline/pkg/cloud/aws/cloudwatch/mocks"
	gosolineLog "github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/justtrackio/gosoline/pkg/metric"
	"github.com/stretchr/testify/mock"
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
	var observedMetricDatums []metric.Datum
	var observedMetricDatumsMutex sync.Mutex
	writer := namespacedMetricWriter{
		namespace: metricNamespace,
		writer:    otelWriter,
		observe: func(batch metric.Data) {
			observedMetricDatumsMutex.Lock()
			defer observedMetricDatumsMutex.Unlock()
			for _, datum := range batch {
				switch datum.MetricName {
				case "requests", "routed.requests", "responses", "routed.responses", MetricHttpRequestsRejected:
					observedMetricDatums = append(observedMetricDatums, *datum)
				}
			}
		},
	}
	definitions := []Definition{
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/widgets/:id"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/auth"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/chaos"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/idle"},
		{Group: &Router{}, HttpMethod: http.MethodGet, RelativePath: "/server-error/:status"},
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
	router.GET("/server-error/:status", func(c *gin.Context) {
		if c.Param("status") == "500" {
			c.AbortWithStatus(http.StatusInternalServerError)

			return
		}

		c.AbortWithStatus(599)
	})

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
	internalServerErrorResponse := httptest.NewRecorder()
	router.ServeHTTP(internalServerErrorResponse, httptest.NewRequest(http.MethodGet, "/server-error/500", http.NoBody))
	require.Equal(t, http.StatusInternalServerError, internalServerErrorResponse.Code)
	serverErrorResponse := httptest.NewRecorder()
	router.ServeHTTP(serverErrorResponse, httptest.NewRequest(http.MethodGet, "/server-error/599", http.NoBody))
	require.Equal(t, 599, serverErrorResponse.Code)
	unmatchedResponse := httptest.NewRecorder()
	router.ServeHTTP(unmatchedResponse, httptest.NewRequest(http.MethodGet, "/missing", http.NoBody))
	require.Equal(t, http.StatusNotFound, unmatchedResponse.Code)

	var exported metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &exported))

	observedMetricDatumsMutex.Lock()
	observedDatums := append([]metric.Datum(nil), observedMetricDatums...)
	observedMetricDatumsMutex.Unlock()
	assertObservedRoutedResponseProjection(t, observedDatums)

	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range exported.ScopeMetrics {
		for _, exportedMetric := range scope.Metrics {
			metrics[exportedMetric.Name] = exportedMetric
		}
	}
	require.Len(t, metrics, 6)

	duration, ok := metrics["gosoline.http.server.request.duration"]
	require.True(t, ok)
	require.Equal(t, "s", duration.Unit)
	durationHistogram, ok := duration.Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, durationHistogram.DataPoints, 7)
	statuses := make(map[string]bool, 7)
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
		case "/server-error/:status":
			require.Contains(t, []string{"500", "599"}, status)
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
		"/widgets/:id:200":          true,
		"/widgets/:id:429":          true,
		"/auth:401":                 true,
		"/chaos:503":                true,
		"/health:200":               true,
		"/server-error/:status:500": true,
		"/server-error/:status:599": true,
	}, statuses)

	requests, ok := metrics["gosoline.http.server.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, requests, map[string]float64{"": 7}, false)

	routedRequests, ok := metrics["gosoline.http.server.routed.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, routedRequests, map[string]float64{
		"/widgets/:id":          2,
		"/auth":                 1,
		"/chaos":                1,
		"/idle":                 0,
		"/health":               1,
		"/server-error/:status": 2,
	}, true)

	rejected, ok := metrics["gosoline.http.server.rejected.requests"]
	require.True(t, ok)
	assertOtelCounterSeries(t, rejected, map[string]float64{
		"/widgets/:id":          1,
		"/auth":                 0,
		"/chaos":                0,
		"/idle":                 0,
		"/server-error/:status": 0,
	}, true)
	responses, ok := metrics["gosoline.http.server.responses"]
	require.True(t, ok)
	assertOtelResponseCounterSeries(t, responses, map[string]float64{
		"200:2xx": 2,
		"429:4xx": 1,
		"401:4xx": 1,
		"503:5xx": 1,
		"500:5xx": 1,
		"599:5xx": 1,
	}, false)

	routedResponses, ok := metrics["gosoline.http.server.routed.responses"]
	require.True(t, ok)
	assertOtelResponseCounterSeries(t, routedResponses, map[string]float64{
		"/widgets/:id:GET:200:2xx":          1,
		"/widgets/:id:GET:429:4xx":          1,
		"/auth:GET:401:4xx":                 1,
		"/chaos:GET:503:5xx":                1,
		"/health:GET:200:2xx":               1,
		"/server-error/:status:GET:500:5xx": 1,
		"/server-error/:status:GET:599:5xx": 1,
	}, true)
	assertOtelRoutedResponseTotal(t, routedResponses)
	assertCloudWatchRoutedResponseProjection(t, logger, observedDatums)
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

func assertOtelResponseCounterSeries(t *testing.T, exported metricdata.Metrics, expectedValues map[string]float64, routed bool) {
	t.Helper()

	require.Equal(t, "{response}", exported.Unit)
	counter, ok := exported.Data.(metricdata.Sum[float64])
	require.True(t, ok)
	require.True(t, counter.IsMonotonic)
	require.Len(t, counter.DataPoints, len(expectedValues))

	actualValues := make(map[string]float64, len(counter.DataPoints))
	for _, point := range counter.DataPoints {
		attributes := point.Attributes.ToSlice()
		statusCode := metricStringAttribute(attributes, dimensionStatusCode)
		statusClass := metricStringAttribute(attributes, dimensionStatusClass)
		if routed {
			route := metricStringAttribute(attributes, dimensionRoute)
			require.ElementsMatch(t, []attribute.KeyValue{
				attribute.String("name", "api"),
				attribute.String("route", route),
				attribute.String("request.method", http.MethodGet),
				attribute.String(dimensionStatusCode, statusCode),
				attribute.String(dimensionStatusClass, statusClass),
			}, attributes)
			actualValues[route+":"+http.MethodGet+":"+statusCode+":"+statusClass] = point.Value
		} else {
			require.ElementsMatch(t, []attribute.KeyValue{
				attribute.String("name", "api"),
				attribute.String(dimensionStatusCode, statusCode),
				attribute.String(dimensionStatusClass, statusClass),
			}, attributes)
			actualValues[statusCode+":"+statusClass] = point.Value
		}
	}
	require.Equal(t, expectedValues, actualValues)
}

func assertObservedRoutedResponseProjection(t *testing.T, datums []metric.Datum) {
	t.Helper()

	var rawRoutedResponseCount, totalRoutedResponseCount int
	routedResponseTotals := make(map[string]float64)
	for _, datum := range datums {
		require.Equal(t, metric.PriorityHigh, datum.Priority)
		require.Equal(t, metric.UnitCount, datum.Unit)
		if datum.MetricName != metricRouteResponses {
			require.Equal(t, metric.KindCounter.Build(), datum.Kind)

			continue
		}

		if datum.Dimensions[dimensionStatusCode] == metric.DimensionDefault {
			totalRoutedResponseCount++
			require.Equal(t, metric.KindTotal, datum.Kind)
			require.Equal(t, 1.0, datum.Value)
			require.Equal(t, metric.Dimensions{
				dimensionServerName:  "api",
				dimensionRoute:       datum.Dimensions[dimensionRoute],
				dimensionMethod:      http.MethodGet,
				dimensionStatusCode:  metric.DimensionDefault,
				dimensionStatusClass: datum.Dimensions[dimensionStatusClass],
			}, datum.Dimensions)
			key := datum.Dimensions[dimensionRoute] + ":" + http.MethodGet + ":" + datum.Dimensions[dimensionStatusClass]
			routedResponseTotals[key] += datum.Value
		} else {
			rawRoutedResponseCount++
			require.Equal(t, metric.KindCounter.Build(), datum.Kind)
			require.Equal(t, 1.0, datum.Value)
			require.NotEqual(t, metric.DimensionDefault, datum.Dimensions[dimensionStatusCode])
		}
	}
	require.Equal(t, 7, rawRoutedResponseCount)
	require.Equal(t, 7, totalRoutedResponseCount)
	require.Equal(t, map[string]float64{
		"/widgets/:id:GET:2xx":          1,
		"/widgets/:id:GET:4xx":          1,
		"/auth:GET:4xx":                 1,
		"/chaos:GET:5xx":                1,
		"/health:GET:2xx":               1,
		"/server-error/:status:GET:5xx": 2,
	}, routedResponseTotals)
}

func assertOtelRoutedResponseTotal(t *testing.T, exported metricdata.Metrics) {
	t.Helper()

	counter, ok := exported.Data.(metricdata.Sum[float64])
	require.True(t, ok)
	var serverErrorOtelCount float64
	for _, point := range counter.DataPoints {
		if metricStringAttribute(point.Attributes.ToSlice(), dimensionRoute) == "/server-error/:status" {
			serverErrorOtelCount += point.Value
		}
	}
	require.Equal(t, 2.0, serverErrorOtelCount)
}

func assertCloudWatchRoutedResponseProjection(t *testing.T, logger gosolineLog.Logger, observedDatums []metric.Datum) {
	t.Helper()

	var cloudwatchData metric.Data
	for i := range observedDatums {
		datum := &observedDatums[i]
		if datum.MetricName == metricRouteResponses && datum.Dimensions[dimensionRoute] == "/server-error/:status" && datum.Dimensions[dimensionStatusClass] == "5xx" {
			cloudwatchData = append(cloudwatchData, datum)
		}
	}
	require.Len(t, cloudwatchData, 4)

	cwClient := cloudwatchMocks.NewClient(t)
	var writtenCloudWatchData []types.MetricDatum
	cwClient.EXPECT().PutMetricData(mock.Anything, mock.MatchedBy(func(input *cloudwatch.PutMetricDataInput) bool {
		writtenCloudWatchData = input.MetricData

		return aws.ToString(input.Namespace) == "test/httpserver"
	})).Return(nil, nil)
	cwWriter := metric.NewCloudwatchWriterWithInterfaces(
		logger,
		clock.NewFakeClockAt(time.Unix(1549283566, 0)),
		cwClient,
		"test/httpserver",
		10*time.Second,
	)
	cwWriter.Write(t.Context(), cloudwatchData)

	require.Len(t, writtenCloudWatchData, 4)
	rawCloudWatchCodes := make(map[string]float64)
	var rawCloudWatchCount, classCloudWatchCount float64
	for _, datum := range writtenCloudWatchData {
		require.Equal(t, "HttpServerRoutedResponses", aws.ToString(datum.MetricName))
		require.Equal(t, metric.UnitCount, datum.Unit)
		dimensions := make(map[string]string, len(datum.Dimensions))
		for _, dimension := range datum.Dimensions {
			dimensions[aws.ToString(dimension.Name)] = aws.ToString(dimension.Value)
		}
		require.Equal(t, "api", dimensions[dimensionServerName])
		require.Equal(t, "/server-error/:status", dimensions[dimensionRoute])
		require.Equal(t, http.MethodGet, dimensions[dimensionMethod])
		require.Equal(t, "5xx", dimensions[dimensionStatusClass])
		if statusCode, hasStatusCode := dimensions[dimensionStatusCode]; hasStatusCode {
			require.Len(t, dimensions, 5)
			rawCloudWatchCodes[statusCode] += aws.ToFloat64(datum.Value)
			rawCloudWatchCount += aws.ToFloat64(datum.Value)
		} else {
			require.Len(t, dimensions, 4)
			classCloudWatchCount += aws.ToFloat64(datum.Value)
		}
	}
	require.Equal(t, map[string]float64{"500": 1, "599": 1}, rawCloudWatchCodes)
	require.Equal(t, 2.0, rawCloudWatchCount)
	require.Equal(t, 2.0, classCloudWatchCount)
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
