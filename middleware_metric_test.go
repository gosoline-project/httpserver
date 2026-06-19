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

func TestMetricMiddleware_WritesRejectedRequestMetrics(t *testing.T) {
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

	rejectedMetrics := writes[1]
	require.Len(t, rejectedMetrics, 2)
	assertRejectedMetric(t, rejectedMetrics[0], metric.Dimensions{
		"Method":     http.MethodGet,
		"Path":       "/widgets/:id",
		"ServerName": "api",
	}, metric.KindDefault)
	assertRejectedMetric(t, rejectedMetrics[1], metric.Dimensions{
		"ServerName": "api",
	}, metric.KindTotal)
}

func TestGetMetricMiddlewareDefaults_IncludesRejectedRequestMetrics(t *testing.T) {
	definition := httpserver.Definition{
		Group:        &httpserver.Router{},
		HttpMethod:   http.MethodGet,
		RelativePath: "/widgets/:id",
	}

	defaults := httpserver.GetMetricMiddlewareDefaults("api", definition)

	require.Len(t, defaults, 4)
	assertDefaultMetric(t, defaults[0], httpserver.MetricHttpRequestCountPerRoute, metric.Dimensions{
		"Method":     http.MethodGet,
		"Path":       "/widgets/:id",
		"ServerName": "api",
	}, metric.KindDefault)
	assertDefaultMetric(t, defaults[1], httpserver.MetricHttpRequestsRejected, metric.Dimensions{
		"Method":     http.MethodGet,
		"Path":       "/widgets/:id",
		"ServerName": "api",
	}, metric.KindDefault)
	assertDefaultMetric(t, defaults[2], httpserver.MetricHttpRequestsRejected, metric.Dimensions{
		"ServerName": "api",
	}, metric.KindTotal)
	assertDefaultMetric(t, defaults[3], httpserver.MetricHttpRequestCount, metric.Dimensions{
		"ServerName": "api",
	}, metric.KindDefault)
}

func assertRejectedMetric(t *testing.T, datum *metric.Datum, dimensions metric.Dimensions, kind metric.Kind) {
	t.Helper()

	assert.Equal(t, metric.PriorityHigh, datum.Priority)
	assert.Equal(t, httpserver.MetricHttpRequestsRejected, datum.MetricName)
	assert.Equal(t, metric.UnitCount, datum.Unit)
	assert.Equal(t, dimensions, datum.Dimensions)
	assert.Equal(t, 1.0, datum.Value)
	assert.Equal(t, kind, datum.Kind)
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
