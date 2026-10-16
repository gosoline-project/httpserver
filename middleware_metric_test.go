package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	httpserverMocks "github.com/gosoline-project/httpserver/mocks"
	"github.com/justtrackio/gosoline/pkg/metric"
	metricMocks "github.com/justtrackio/gosoline/pkg/metric/mocks"
	"github.com/justtrackio/gosoline/pkg/test/matcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMetricMiddleware_WritesCanonicalRequestMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writes := make([]metric.Data, 0)
	writer := metricMocks.NewWriter(t)
	writer.EXPECT().Write(matcher.Context, mock.Anything).Run(func(_ context.Context, batch metric.Data) {
		writes = append(writes, batch)
	}).Return().Twice()
	recorder := httpserverMocks.NewServerMetricRecorder(t)
	recorder.EXPECT().TrackRequestStarted(matcher.Context).Return().Once()
	recorder.EXPECT().TrackRequestCompleted(matcher.Context).Return().Once()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		httpserver.MetricMiddleware("api", c, writer, recorder)
	})
	router.Use(func(c *gin.Context) {
		c.Request = httpserver.MarkRequestRejected(c.Request)
		c.AbortWithStatus(http.StatusTooManyRequests)
	})
	router.GET("/widgets/:id", func(c *gin.Context) {})

	request := httptest.NewRequest(http.MethodGet, "/widgets/42", http.NoBody)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Len(t, writes, 2)

	durationMetrics := writes[0]
	require.Len(t, durationMetrics, 1)
	assert.Equal(t, httpserver.MetricHttpRequestDuration, durationMetrics[0].MetricName)
	assert.Equal(t, metric.Dimensions{
		"name":                 "api",
		"route":                "/widgets/:id",
		"request.method":       http.MethodGet,
		"response.status_code": "429",
	}, durationMetrics[0].Dimensions)
	assert.Equal(t, metric.UnitMillisecondsAverage, durationMetrics[0].Unit)
	assert.Equal(t, metric.KindHistogram.Build(), durationMetrics[0].Kind)

	rejectedMetrics := writes[1]
	require.Len(t, rejectedMetrics, 1)
	assertRejectedMetric(t, rejectedMetrics[0], metric.Dimensions{
		"name":           "api",
		"route":          "/widgets/:id",
		"request.method": http.MethodGet,
	})
}

func TestGetMetricMiddlewareDefaults_IncludesRejectedRequestMetrics(t *testing.T) {
	definition := httpserver.Definition{
		Group:        &httpserver.Router{},
		HttpMethod:   http.MethodGet,
		RelativePath: "/widgets/:id",
	}

	defaults := httpserver.GetMetricMiddlewareDefaults("api", definition)

	require.Len(t, defaults, 1)
	assertDefaultMetric(t, defaults[0], httpserver.MetricHttpRequestsRejected, metric.Dimensions{
		"name":           "api",
		"route":          "/widgets/:id",
		"request.method": http.MethodGet,
	}, metric.KindCounter.Build())
}

func assertRejectedMetric(t *testing.T, datum *metric.Datum, dimensions metric.Dimensions) {
	t.Helper()

	assert.Equal(t, metric.PriorityHigh, datum.Priority)
	assert.Equal(t, httpserver.MetricHttpRequestsRejected, datum.MetricName)
	assert.Equal(t, metric.UnitCount, datum.Unit)
	assert.Equal(t, dimensions, datum.Dimensions)
	assert.Equal(t, 1.0, datum.Value)
	assert.Equal(t, metric.KindCounter.Build(), datum.Kind)
}

func assertDefaultMetric(t *testing.T, datum *metric.Datum, metricName string, dimensions metric.Dimensions, kind metric.Kind) {
	t.Helper()

	assert.Equal(t, metric.PriorityHigh, datum.Priority)
	assert.Equal(t, metricName, datum.MetricName)
	assert.Equal(t, dimensions, datum.Dimensions)
	assert.Equal(t, metric.UnitCount, datum.Unit)
	assert.Equal(t, 0.0, datum.Value)
	assert.Equal(t, kind, datum.Kind)
}
