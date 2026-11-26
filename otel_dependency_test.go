package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justtrackio/gosoline/pkg/appctx"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/metric"
	"github.com/justtrackio/gosoline/pkg/otel"
	"github.com/justtrackio/gosoline/pkg/tracing"
	"github.com/stretchr/testify/require"
	otelglobal "go.opentelemetry.io/otel"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type otlpMetricReceiver struct {
	collectormetrics.UnimplementedMetricsServiceServer
	requests chan *collectormetrics.ExportMetricsServiceRequest
}

func (r *otlpMetricReceiver) Export(_ context.Context, request *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	r.requests <- request

	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

type otlpTraceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	requests chan *collectortrace.ExportTraceServiceRequest
}

func (r *otlpTraceReceiver) Export(_ context.Context, request *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	r.requests <- request

	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func otlpTestReceiver(t *testing.T, protocol string) (endpoint string, traceRequests <-chan *collectortrace.ExportTraceServiceRequest, metricRequests <-chan *collectormetrics.ExportMetricsServiceRequest) {
	t.Helper()
	traces := make(chan *collectortrace.ExportTraceServiceRequest, 2)
	metrics := make(chan *collectormetrics.ExportMetricsServiceRequest, 2)

	if protocol == otel.ProtocolGrpc {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		server := grpc.NewServer()
		collectormetrics.RegisterMetricsServiceServer(server, &otlpMetricReceiver{requests: metrics})
		collectortrace.RegisterTraceServiceServer(server, &otlpTraceReceiver{requests: traces})
		served := make(chan error, 1)
		go func() { served <- server.Serve(listener) }()
		t.Cleanup(func() {
			server.Stop()
			require.NoError(t, <-served)
		})

		return listener.Addr().String(), traces, metrics
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var err error
		if body, err = io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		switch r.URL.Path {
		case "/v1/traces":
			request := new(collectortrace.ExportTraceServiceRequest)
			if err := proto.Unmarshal(body, request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			traces <- request
		case "/v1/metrics":
			request := new(collectormetrics.ExportMetricsServiceRequest)
			if err := proto.Unmarshal(body, request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			metrics <- request
		default:
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(server.Close)

	return strings.TrimPrefix(server.URL, "http://"), traces, metrics
}

func TestSelectedOtelProvidersExport(t *testing.T) {
	for _, protocol := range []string{otel.ProtocolHttp, otel.ProtocolGrpc} {
		t.Run(protocol, func(t *testing.T) {
			endpoint, traces, metrics := otlpTestReceiver(t, protocol)
			traceExporter := tracing.ExporterOtelHttp
			if protocol == otel.ProtocolGrpc {
				traceExporter = tracing.ExporterOtelGrpc
			}
			config := cfg.New(map[string]any{
				"app": map[string]any{"env": "test", "name": "httpserver-test", "namespace": "tests"},
				"tracing": map[string]any{"otel": map[string]any{
					"exporter": traceExporter, "sampling_ratio": 1.0,
					"http": map[string]any{"endpoint": endpoint, "insecure": true, "compression": false},
				}},
				"otel": map[string]any{"exporter": map[string]any{
					"protocol": protocol, "endpoint": endpoint, "insecure": true, "compression": "none",
				}},
				"metric": map[string]any{"writer_settings": map[string]any{"otel": map[string]any{"interval": "1h"}}},
			})
			ctx := appctx.WithContainer(context.Background())
			logger := log.NewLogger()
			previousProvider := otelglobal.GetTracerProvider()
			previousPropagator := otelglobal.GetTextMapPropagator()
			t.Cleanup(func() {
				otelglobal.SetTracerProvider(previousProvider)
				otelglobal.SetTextMapPropagator(previousPropagator)
			})

			provider, err := tracing.ProvideOtelTraceProvider(ctx, config, logger)
			require.NoError(t, err)
			traceShutdown, err := tracing.ProvideShutdownHandler(ctx, config, logger)
			require.NoError(t, err)
			traceStopped := false
			t.Cleanup(func() {
				if !traceStopped {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					require.NoError(t, traceShutdown.Shutdown(cleanupCtx))
				}
			})
			writer, err := metric.NewOtelWriter(ctx, config, logger)
			require.NoError(t, err)
			metricShutdown, err := metric.ProvideShutdownHandler(ctx, config, logger)
			require.NoError(t, err)
			metricStopped := false
			t.Cleanup(func() {
				if !metricStopped {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					require.NoError(t, metricShutdown.Shutdown(cleanupCtx))
				}
			})

			_, span := provider.Tracer("httpserver-test").Start(ctx, "test-span")
			span.End()
			writer.WriteOne(ctx, &metric.Datum{
				Namespace: "httpserver.test", MetricName: "requests", Priority: metric.PriorityHigh,
				Dimensions: metric.Dimensions{"route": "/example"}, Value: 1, Unit: metric.UnitCount,
				Kind: metric.KindCounter.Build(),
			})
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			require.NoError(t, traceShutdown.Shutdown(shutdownCtx))
			traceStopped = true
			require.NoError(t, metricShutdown.Shutdown(shutdownCtx))
			metricStopped = true

			select {
			case request := <-traces:
				resources := request.GetResourceSpans()
				require.Len(t, resources, 1)
				require.Equal(t, "https://opentelemetry.io/schemas/1.43.0", resources[0].GetSchemaUrl())
				require.Len(t, resources[0].GetScopeSpans(), 1)
				require.Len(t, resources[0].GetScopeSpans()[0].GetSpans(), 1)
				require.Equal(t, "test-span", resources[0].GetScopeSpans()[0].GetSpans()[0].GetName())
			case <-shutdownCtx.Done():
				t.Fatal(shutdownCtx.Err())
			}
			select {
			case request := <-metrics:
				resources := request.GetResourceMetrics()
				require.Len(t, resources, 1)
				require.Equal(t, "https://opentelemetry.io/schemas/1.43.0", resources[0].GetSchemaUrl())
				require.Len(t, resources[0].GetScopeMetrics(), 1)
				require.Len(t, resources[0].GetScopeMetrics()[0].GetMetrics(), 1)
				points := resources[0].GetScopeMetrics()[0].GetMetrics()[0].GetSum().GetDataPoints()
				require.Len(t, points, 1)
				attributes := points[0].GetAttributes()
				require.Len(t, attributes, 1)
				require.Equal(t, "route", attributes[0].GetKey())
				require.Equal(t, "/example", attributes[0].GetValue().GetStringValue())
			case <-shutdownCtx.Done():
				t.Fatal(shutdownCtx.Err())
			}
		})
	}
}
