package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/justtrackio/gosoline/pkg/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	common "go.opentelemetry.io/proto/otlp/common/v1"
)

// Both OTLP transports must serialize the structured attribute and integer
// histogram with the same selected exporter family as the Gosoline provider.
func TestSelectedOtelMetricExportersMapAndIntegerHistogram(t *testing.T) {
	for _, protocol := range []string{otel.ProtocolHttp, otel.ProtocolGrpc} {
		t.Run(protocol, func(t *testing.T) {
			endpoint, _, requests := otlpTestReceiver(t, protocol)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			exporter, err := otel.BuildMetricExporter(ctx, otel.ExporterSettings{
				Protocol: protocol, Endpoint: endpoint, Insecure: true, Compression: "none", Timeout: 10 * time.Second,
			})
			require.NoError(t, err)
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour))))
			stopped := false
			t.Cleanup(func() {
				if !stopped {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cleanupCancel()
					require.NoError(t, provider.Shutdown(cleanupCtx))
				}
			})
			histogram, err := provider.Meter("httpserver-map-test").Int64Histogram("requests.size")
			require.NoError(t, err)
			histogram.Record(ctx, 42, otelmetric.WithAttributes(attribute.Map("nested", attribute.String("key", "value"), attribute.Map("child", attribute.Int("count", 7)))))
			require.NoError(t, provider.Shutdown(ctx))
			stopped = true

			select {
			case request := <-requests:
				resources := request.GetResourceMetrics()
				require.Len(t, resources, 1)
				require.Equal(t, "https://opentelemetry.io/schemas/1.43.0", resources[0].GetSchemaUrl())
				require.Len(t, resources[0].GetScopeMetrics(), 1)
				measurements := resources[0].GetScopeMetrics()[0].GetMetrics()
				require.Len(t, measurements, 1)
				require.Equal(t, "requests.size", measurements[0].GetName())
				points := measurements[0].GetHistogram().GetDataPoints()
				require.Len(t, points, 1)
				require.EqualValues(t, 1, points[0].GetCount())
				require.Equal(t, float64(42), points[0].GetSum())
				require.Len(t, points[0].GetAttributes(), 1)
				require.Equal(t, "nested", points[0].GetAttributes()[0].GetKey())
				values := make(map[string]*common.AnyValue)
				for _, entry := range points[0].GetAttributes()[0].GetValue().GetKvlistValue().GetValues() {
					values[entry.GetKey()] = entry.GetValue()
				}
				require.Len(t, values, 2)
				require.Equal(t, "value", values["key"].GetStringValue())
				child := values["child"].GetKvlistValue().GetValues()
				require.Len(t, child, 1)
				require.Equal(t, "count", child[0].GetKey())
				require.EqualValues(t, 7, child[0].GetValue().GetIntValue())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
