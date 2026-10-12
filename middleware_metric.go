package httpserver

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/metric"
)

const (
	// MetricHttpRequestDuration records request duration as a histogram; its observation count is
	// the request count and its dimensions distinguish routes, methods and response statuses.
	MetricHttpRequestDuration = "request.duration"
	// MetricHttpRequestsRejected is the count of requests rejected before handling.
	MetricHttpRequestsRejected = "rejected.requests"

	dimensionServerName = "name"
	dimensionRoute      = "route"
	dimensionMethod     = "request.method"
	dimensionStatusCode = "response.status_code"
)

// NewMetricMiddleware creates request metrics middleware and a setup hook for route defaults.
func NewMetricMiddleware(name string, metricRecorder ServerMetricRecorder) (middleware gin.HandlerFunc, setupHandler func(definitions []Definition)) {
	// writer without any defaults until we initialize some defaults and overwrite it
	writer := metric.NewWriter(metricNamespace)

	middleware = func(ginCtx *gin.Context) {
		MetricMiddleware(name, ginCtx, writer, metricRecorder)
	}

	setupHandler = func(definitions []Definition) {
		defaults := GetMetricMiddlewareDefaults(name, definitions...)
		writer = metric.NewWriter(metricNamespace, defaults...)
	}

	return middleware, setupHandler
}

// MetricMiddleware records request metrics for a Gin context.
func MetricMiddleware(name string, ginCtx *gin.Context, writer metric.Writer, metricRecorder ServerMetricRecorder) {
	start := time.Now()
	method := ginCtx.Request.Method

	metricRecorder.TrackRequestStarted(ginCtx.Request.Context())
	defer metricRecorder.TrackRequestCompleted(ginCtx.Request.Context())

	path := ginCtx.FullPath()
	if path == "" {
		// the path was not found, so no need to print anything
		return
	}

	path = trimRightPath(path)
	path = removeDuplicates(path)

	ginCtx.Next()

	requestTimeNano := time.Since(start)
	requestTimeMillisecond := float64(requestTimeNano) / float64(time.Millisecond)

	routeDimensions := metric.Dimensions{
		dimensionServerName: name,
		dimensionRoute:      path,
		dimensionMethod:     method,
		dimensionStatusCode: strconv.Itoa(ginCtx.Writer.Status()),
	}

	writer.Write(ginCtx.Request.Context(), metric.Data{
		{
			Priority:   metric.PriorityHigh,
			MetricName: MetricHttpRequestDuration,
			Dimensions: routeDimensions,
			Unit:       metric.UnitMillisecondsAverage,
			Value:      requestTimeMillisecond,
			Kind:       metric.KindHistogram.Build(),
		},
	})

	if WasRequestRejected(ginCtx.Request) {
		writer.Write(ginCtx.Request.Context(), metric.Data{
			{
				Priority:   metric.PriorityHigh,
				MetricName: MetricHttpRequestsRejected,
				Dimensions: metric.Dimensions{
					dimensionServerName: name,
					dimensionRoute:      path,
					dimensionMethod:     method,
				},
				Unit:  metric.UnitCount,
				Value: 1.0,
				Kind:  metric.KindCounter.Build(),
			},
		})
	}
}

// GetMetricMiddlewareDefaults returns zero-valued rejected-request counters for registered routes.
func GetMetricMiddlewareDefaults(name string, definitions ...Definition) metric.Data {
	return funk.Map(definitions, func(definition Definition) *metric.Datum {
		return &metric.Datum{
			Priority:   metric.PriorityHigh,
			MetricName: MetricHttpRequestsRejected,
			Dimensions: metric.Dimensions{
				dimensionServerName: name,
				dimensionRoute:      definition.getAbsolutePath(),
				dimensionMethod:     definition.HttpMethod,
			},
			Unit:  metric.UnitCount,
			Value: 0.0,
			Kind:  metric.KindCounter.Build(),
		}
	})
}
