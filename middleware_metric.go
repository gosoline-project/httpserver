package httpserver

import (
	"fmt"
	"path"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/metric"
)

const (
	perRoute                       = "PerRoute"
	MetricHttpRequestCount         = "HttpRequestCount"
	MetricHttpRequestCountPerRoute = "HttpRequestCountPerRoute"
	MetricHttpRequestResponseTime  = "HttpRequestResponseTime"
	MetricHttpStatus               = "HttpStatus"
)

func NewMetricMiddleware(name string) (middleware fiber.Handler, setupHandler func(app *fiber.App)) {
	// writer without any defaults until we initialize some defaults and overwrite it
	writer := metric.NewWriter()

	middleware = func(reqCtx fiber.Ctx) error {
		return metricMiddleware(name, reqCtx, writer)
	}

	setupHandler = func(app *fiber.App) {
		defaults := getMetricMiddlewareDefaults(name, app.GetRoutes()...)
		writer = metric.NewWriter(defaults...)
	}

	return middleware, setupHandler
}

func metricMiddleware(name string, reqCtx fiber.Ctx, writer metric.Writer) error {
	start := time.Now()
	method := reqCtx.Method()

	reqPath := reqCtx.Path()
	if reqPath == "" {
		// the path was not found, so no need to print anything
		return reqCtx.Next()
	}

	chainErr := reqCtx.Next()

	reqPath = path.Clean(reqPath)
	requestTimeNano := time.Since(start)
	requestTimeMillisecond := float64(requestTimeNano) / float64(time.Millisecond)

	status := reqCtx.Response().StatusCode() / 100
	statusMetric := fmt.Sprintf("%s%dXX", MetricHttpStatus, status)

	writer.Write(reqCtx.Context(), createMetricsWithDimensions(metric.Data{
		{
			Priority:   metric.PriorityHigh,
			MetricName: MetricHttpRequestResponseTime,
			Unit:       metric.UnitMillisecondsAverage,
			Value:      requestTimeMillisecond,
		},
		{
			Priority:   metric.PriorityHigh,
			MetricName: MetricHttpRequestCount,
			Unit:       metric.UnitCount,
			Value:      1.0,
		},
		{
			Priority:   metric.PriorityHigh,
			MetricName: statusMetric,
			Unit:       metric.UnitCount,
			Value:      1.0,
		},
	}, map[string]metric.Dimensions{
		perRoute: {
			"Method":     method,
			"Path":       reqPath,
			"ServerName": name,
		},
		"": {
			"ServerName": name,
		},
	}))

	return chainErr
}

// createMetricsWithDimensions is creating a metric.Data set
// which included each provided metric with each provided set of dimensions.
// The key of the dimensions map is appended to the metric name, so the name is unique across set of dimensions
func createMetricsWithDimensions(metrics metric.Data, dimensionsByMetricSuffix map[string]metric.Dimensions) metric.Data {
	return funk.Flatten(funk.Map(metrics, func(metricDatum *metric.Datum) metric.Data {
		data := make(metric.Data, 0)
		for metricNameExtension, dimensions := range dimensionsByMetricSuffix {
			datum := *metricDatum
			datum.MetricName += metricNameExtension
			datum.Dimensions = dimensions

			data = append(data, &datum)
		}

		return data
	}))
}

func getMetricMiddlewareDefaults(name string, definitions ...fiber.Route) metric.Data {
	return append(funk.Map(definitions, func(route fiber.Route) *metric.Datum {
		return &metric.Datum{
			Priority:   metric.PriorityHigh,
			MetricName: MetricHttpRequestCountPerRoute,
			Dimensions: metric.Dimensions{
				"Method":     route.Method,
				"Path":       route.Path,
				"ServerName": name,
			},
			Unit:  metric.UnitCount,
			Value: 0.0,
		}
	}), &metric.Datum{
		Priority:   metric.PriorityHigh,
		MetricName: MetricHttpRequestCount,
		Dimensions: metric.Dimensions{
			"ServerName": name,
		},
		Unit:  metric.UnitCount,
		Value: 0.0,
	})
}
