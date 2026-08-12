package httpserver

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/appctx"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testResponseNegotiator struct{}

func (*testResponseNegotiator) Render(*http.Request, any) (Response, error) {
	return nil, nil
}

func TestNewServerOptionsUsesDefaultResponseNegotiator(t *testing.T) {
	options := newServerOptions()

	assert.IsType(t, &ContentNegotiator{}, options.responseNegotiator)
}

func TestNewServerOptionsUsesConfiguredResponseNegotiator(t *testing.T) {
	negotiator := &testResponseNegotiator{}

	options := newServerOptions(WithResponseNegotiator(negotiator))

	assert.Same(t, negotiator, options.responseNegotiator)
}

func TestNewServerWithSettingsAcceptsResponseNegotiatorOption(t *testing.T) {
	settings := &Settings{}
	negotiator := &testResponseNegotiator{}

	factory := NewServerWithSettings(t.Context(), "test", nil, settings, WithResponseNegotiator(negotiator))

	assert.NotNil(t, factory)
	assert.Equal(t, "test", settings.Name)
}

func TestWithResponseNegotiatorPanicsForNil(t *testing.T) {
	assert.Panics(t, func() {
		WithResponseNegotiator(nil)
	})
}

func TestWithErrorHandlerAssignsHandlerDirectly(t *testing.T) {
	original := defaultErrorHandler
	t.Cleanup(func() {
		defaultErrorHandler = original
	})

	WithErrorHandler(func(_ int, _ error) any {
		return "custom"
	})

	assert.Equal(t, "custom", defaultErrorHandler(0, errors.New("ignored")))
}

func TestWithErrorHandlerAssignsNilDirectly(t *testing.T) {
	original := defaultErrorHandler
	t.Cleanup(func() {
		defaultErrorHandler = original
	})

	WithErrorHandler(nil)

	assert.Nil(t, defaultErrorHandler)
}

type serverOptionResponse struct {
	XMLName xml.Name `xml:"response" json:"-"`
	Message string   `json:"message" xml:"message"`
}

func TestNewServerWithSettingsUsesConfiguredResponseNegotiator(t *testing.T) {
	negotiator, err := NewContentNegotiator(
		ContentTypeApplicationJson,
		JSONRepresentation(),
		XMLRepresentation(),
	)
	require.NoError(t, err)

	server := buildServerForOptionsTest(t, NewServerWithSettings(
		t.Context(),
		"test",
		serverOptionRouterFactory,
		&Settings{
			Port: "0",
			Mode: gin.TestMode,
			Compression: CompressionSettings{
				Level: "none",
			},
		},
		WithResponseNegotiator(negotiator),
	))

	recorder := serveServerOptionsTestRequest(t, server, ContentTypeApplicationXml)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, ContentTypeXml, recorder.Header().Get(HeaderContentType))
	assert.Equal(t, `<response><message>hello</message></response>`, recorder.Body.String())
}

func TestNewServerWithSettingsUsesJSONNegotiatorByDefault(t *testing.T) {
	server := buildServerForOptionsTest(t, NewServerWithSettings(
		t.Context(),
		"test",
		serverOptionRouterFactory,
		&Settings{
			Port: "0",
			Mode: gin.TestMode,
			Compression: CompressionSettings{
				Level: "none",
			},
		},
	))

	recorder := serveServerOptionsTestRequest(t, server, ContentTypeApplicationXml)

	assert.Equal(t, http.StatusNotAcceptable, recorder.Code)
	assert.Equal(t, ContentTypeJson, recorder.Header().Get(HeaderContentType))
	assert.Contains(t, recorder.Body.String(), "not acceptable")
}

func TestNewServerUsesConfiguredResponseNegotiator(t *testing.T) {
	negotiator, err := NewContentNegotiator(
		ContentTypeApplicationJson,
		JSONRepresentation(),
		XMLRepresentation(),
	)
	require.NoError(t, err)

	server := buildServerForOptionsTest(t, NewServer(
		"test",
		serverOptionRouterFactory,
		WithResponseNegotiator(negotiator),
	))

	recorder := serveServerOptionsTestRequest(t, server, ContentTypeApplicationXml)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, ContentTypeXml, recorder.Header().Get(HeaderContentType))
	assert.Equal(t, `<response><message>hello</message></response>`, recorder.Body.String())
}

func serverOptionRouterFactory(_ context.Context, _ cfg.Config, _ log.Logger, router *Router) error {
	router.GET("/result", BindN(func(context.Context) (serverOptionResponse, error) {
		return serverOptionResponse{Message: "hello"}, nil
	}))

	return nil
}

func buildServerForOptionsTest(t *testing.T, factory kernel.ModuleFactory) *HttpServer {
	t.Helper()

	ctx := appctx.WithContainer(t.Context())
	config := cfg.New(map[string]any{
		"httpserver": map[string]any{
			"test": map[string]any{
				"port": "0",
			},
		},
		"tracing": map[string]any{
			"provider": "noop",
		},
	})
	logger := log.NewLogger()
	var server *HttpServer

	_, err := kernel.BuildKernel(ctx, config, logger, []kernel.Option{
		kernel.WithModuleFactory("httpserver-test", func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
			module, err := factory(ctx, config, logger)
			if err != nil {
				return nil, err
			}

			server = module.(*HttpServer)

			return server, nil
		}),
	})
	require.NoError(t, err)
	require.NotNil(t, server)

	t.Cleanup(func() {
		assert.NoError(t, server.listener.Close())
	})

	return server
}

func serveServerOptionsTestRequest(t *testing.T, server *HttpServer, accept string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/result", http.NoBody)
	request.Header.Set(HeaderAccept, accept)
	server.server.Handler.ServeHTTP(recorder, request)

	return recorder
}
