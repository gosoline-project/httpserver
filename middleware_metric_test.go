package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	httpserverMocks "github.com/gosoline-project/httpserver/mocks"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/metric"
	metricMocks "github.com/justtrackio/gosoline/pkg/metric/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMetricMiddlewareEmitsCanonicalMetricData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	writer := metricMocks.NewWriter(t)
	var batches []metric.Data
	writer.EXPECT().Write(mock.Anything, mock.Anything).Run(func(_ context.Context, data metric.Data) {
		batches = append(batches, append(metric.Data(nil), data...))
	}).Return().Times(6)

	recorder := httpserverMocks.NewServerMetricRecorder(t)
	recorder.EXPECT().TrackRequestStarted(mock.Anything).Return().Times(7)
	recorder.EXPECT().TrackRequestCompleted(mock.Anything).Return().Times(7)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		httpserver.MetricMiddleware("api", c, writer, recorder)
	})
	router.GET("/widgets/:id", func(c *gin.Context) {
		time.Sleep(20 * time.Millisecond)
	})
	router.GET("/auth", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	router.GET("/rejected", func(c *gin.Context) {
		c.Request = httpserver.MarkRequestRejected(c.Request)
		c.AbortWithStatus(http.StatusTooManyRequests)
	})
	router.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/server-error/:status", func(c *gin.Context) {
		statusCode, err := strconv.Atoi(c.Param("status"))
		require.NoError(t, err)
		c.AbortWithStatus(statusCode)
	})

	for _, testCase := range []struct {
		path       string
		statusCode int
	}{
		{
			path:       "/widgets/42",
			statusCode: http.StatusOK,
		},
		{
			path:       "/auth",
			statusCode: http.StatusUnauthorized,
		},
		{
			path:       "/rejected",
			statusCode: http.StatusTooManyRequests,
		},
		{
			path:       "/server-error/500",
			statusCode: http.StatusInternalServerError,
		},
		{
			path:       "/server-error/599",
			statusCode: 599,
		},
		{
			path:       "/health",
			statusCode: http.StatusNoContent,
		},
		{
			path:       "/missing",
			statusCode: http.StatusNotFound,
		},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, testCase.path, http.NoBody))
		require.Equal(t, testCase.statusCode, response.Code)
	}

	require.Len(t, batches, 6)
	duration := assertMetricMiddlewareBatch(t, batches[0], "/widgets/:id", "200", "2xx", false)
	require.Greater(t, duration, 10.0)
	assertMetricMiddlewareBatch(t, batches[1], "/auth", "401", "4xx", false)
	assertMetricMiddlewareBatch(t, batches[2], "/rejected", "429", "4xx", true)
	assertMetricMiddlewareBatch(t, batches[3], "/server-error/:status", "500", "5xx", false)
	assertMetricMiddlewareBatch(t, batches[4], "/server-error/:status", "599", "5xx", false)
	assertMetricMiddlewareBatch(t, batches[5], "/health", "204", "2xx", false)

	fiveHundredRaw := findMetricDatum(batches[3], "routed.responses", metric.Dimensions{
		"name":                  "api",
		"route":                 "/server-error/:status",
		"request.method":        http.MethodGet,
		"response.status_code":  "500",
		"response.status_class": "5xx",
	})
	fiveNinetyNineRaw := findMetricDatum(batches[4], "routed.responses", metric.Dimensions{
		"name":                  "api",
		"route":                 "/server-error/:status",
		"request.method":        http.MethodGet,
		"response.status_code":  "599",
		"response.status_class": "5xx",
	})
	require.NotNil(t, fiveHundredRaw)
	require.NotNil(t, fiveNinetyNineRaw)
	require.Equal(t, 1.0, fiveHundredRaw.Value)
	require.Equal(t, 1.0, fiveNinetyNineRaw.Value)
	require.NotEqual(t, fiveHundredRaw.Dimensions, fiveNinetyNineRaw.Dimensions)

	fiveXProjectionDimensions := metric.Dimensions{
		"name":                  "api",
		"route":                 "/server-error/:status",
		"request.method":        http.MethodGet,
		"response.status_code":  metric.DimensionDefault,
		"response.status_class": "5xx",
	}
	fiveHundredProjection := findMetricDatum(batches[3], "routed.responses", fiveXProjectionDimensions)
	fiveNinetyNineProjection := findMetricDatum(batches[4], "routed.responses", fiveXProjectionDimensions)
	require.NotNil(t, fiveHundredProjection)
	require.NotNil(t, fiveNinetyNineProjection)
	require.Equal(t, metric.KindTotal, fiveHundredProjection.Kind)
	require.Equal(t, metric.KindTotal, fiveNinetyNineProjection.Kind)
	require.Equal(t, fiveHundredProjection.Dimensions, fiveNinetyNineProjection.Dimensions)
}

func assertMetricMiddlewareBatch(t *testing.T, data metric.Data, route, statusCode, statusClass string, rejected bool) float64 {
	t.Helper()

	expectedLength := 6
	if rejected {
		expectedLength += 2
	}
	require.Len(t, data, expectedLength)

	routeDimensions := metric.Dimensions{
		"name":           "api",
		"route":          route,
		"request.method": http.MethodGet,
	}
	responseDimensions := metric.Dimensions{
		"name":                  "api",
		"response.status_code":  statusCode,
		"response.status_class": statusClass,
	}
	routedResponseDimensions := metric.Dimensions{
		"name":                  "api",
		"route":                 route,
		"request.method":        http.MethodGet,
		"response.status_code":  statusCode,
		"response.status_class": statusClass,
	}
	durationDimensions := metric.Dimensions{
		"name":                 "api",
		"route":                route,
		"request.method":       http.MethodGet,
		"response.status_code": statusCode,
	}

	duration := findMetricDatum(data, "request.duration", durationDimensions)
	require.NotNil(t, duration)
	durationValue := duration.Value
	actualDuration := *duration
	actualDuration.Value = 0
	require.Equal(t, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "request.duration",
		Dimensions: durationDimensions,
		Unit:       metric.UnitMillisecondsAverage,
		Kind: metric.KindHistogram.WithBuckets([]float64{
			5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000,
		}).Build(),
	}, actualDuration)
	require.GreaterOrEqual(t, durationValue, 0.0)

	assertMetricDatum(t, data, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "responses",
		Dimensions: responseDimensions,
		Unit:       metric.UnitCount,
		Value:      1,
		Kind:       metric.KindCounter.Build(),
	})
	assertMetricDatum(t, data, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "routed.responses",
		Dimensions: routedResponseDimensions,
		Unit:       metric.UnitCount,
		Value:      1,
		Kind:       metric.KindCounter.Build(),
	})
	assertMetricDatum(t, data, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "routed.responses",
		Dimensions: metric.Dimensions{
			"name":                  "api",
			"route":                 route,
			"request.method":        http.MethodGet,
			"response.status_code":  metric.DimensionDefault,
			"response.status_class": statusClass,
		},
		Unit:  metric.UnitCount,
		Value: 1,
		Kind:  metric.KindTotal,
	})
	assertMetricDatum(t, data, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "requests",
		Dimensions: metric.Dimensions{"name": "api"},
		Unit:       metric.UnitCount,
		Value:      1,
		Kind:       metric.KindCounter.Build(),
	})
	assertMetricDatum(t, data, metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: "routed.requests",
		Dimensions: routeDimensions,
		Unit:       metric.UnitCount,
		Value:      1,
		Kind:       metric.KindCounter.Build(),
	})

	if rejected {
		assertMetricDatum(t, data, metric.Datum{
			Priority:   metric.PriorityHigh,
			MetricName: "rejected.requests",
			Dimensions: routeDimensions,
			Unit:       metric.UnitCount,
			Value:      1,
			Kind:       metric.KindCounter.Build(),
		})
		assertMetricDatum(t, data, metric.Datum{
			Priority:   metric.PriorityHigh,
			MetricName: "rejected.requests",
			Dimensions: metric.Dimensions{"name": "api"},
			Unit:       metric.UnitCount,
			Value:      1,
			Kind:       metric.KindTotal,
		})
	}

	return durationValue
}

func assertMetricDatum(t *testing.T, data metric.Data, expected metric.Datum) {
	t.Helper()

	actual := findMetricDatum(data, expected.MetricName, expected.Dimensions)
	require.NotNil(t, actual, "metric %q with dimensions %#v was not emitted", expected.MetricName, expected.Dimensions)
	require.Equal(t, expected, *actual)
}

func findMetricDatum(data metric.Data, metricName string, dimensions metric.Dimensions) *metric.Datum {
	datum, ok := funk.FindFirstFunc(data, func(datum *metric.Datum) bool {
		return datum != nil && datum.MetricName == metricName && reflect.DeepEqual(datum.Dimensions, dimensions)
	})
	if !ok {
		return nil
	}

	return datum
}
