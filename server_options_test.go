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
	options, err := newServerOptions()

	require.NoError(t, err)
	assert.IsType(t, &ContentNegotiator{}, options.responseNegotiator)
}

func TestNewServerOptionsUsesConfiguredResponseNegotiator(t *testing.T) {
	negotiator := &testResponseNegotiator{}

	options, err := newServerOptions(WithResponseNegotiator(negotiator))

	require.NoError(t, err)
	assert.Same(t, negotiator, options.responseNegotiator)
}

func TestNewServerWithSettingsAcceptsResponseNegotiatorOption(t *testing.T) {
	settings := &Settings{}
	negotiator := &testResponseNegotiator{}

	factory := NewServerWithSettings(t.Context(), "test", nil, settings, WithResponseNegotiator(negotiator))

	assert.NotNil(t, factory)
	assert.Equal(t, "test", settings.Name)
}

func TestNewServerWithSettingsReturnsOptionError(t *testing.T) {
	expectedErr := errors.New("option failed")
	factory := NewServerWithSettings(t.Context(), "test", nil, &Settings{}, func(*serverOptions) error {
		return expectedErr
	})

	_, err := factory(t.Context(), cfg.New(map[string]any{}), log.NewLogger())

	require.ErrorIs(t, err, expectedErr)
	require.ErrorContains(t, err, "could not configure server options")
}

func TestWithResponseNegotiatorReturnsErrorForNil(t *testing.T) {
	_, err := newServerOptions(WithResponseNegotiator(nil))

	require.EqualError(t, err, "could not apply server option: response negotiator is required")
}

func TestNewServerOptionsReturnsErrorForNilOption(t *testing.T) {
	_, err := newServerOptions(nil)

	require.EqualError(t, err, "server option is required")
}

func TestNewServerOptionsUsesDefaultErrorHandler(t *testing.T) {
	options, err := newServerOptions()

	require.NoError(t, err)
	assert.Equal(t, errorResponseBody{Err: "ignored"}, options.errorHandler(0, errors.New("ignored")))
}

func TestNewServerOptionsUsesConfiguredErrorHandler(t *testing.T) {
	handler := func(_ int, _ error) any {
		return "custom"
	}

	options, err := newServerOptions(WithErrorHandler(handler))

	require.NoError(t, err)
	assert.Equal(t, "custom", options.errorHandler(0, errors.New("ignored")))
}

func TestWithErrorHandlerReturnsErrorForNil(t *testing.T) {
	_, err := newServerOptions(WithErrorHandler(nil))

	require.EqualError(t, err, "could not apply server option: error handler is required")
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
	router.GET("/error", BindN(func(context.Context) (serverOptionResponse, error) {
		return serverOptionResponse{}, errors.New("server error")
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
			var module kernel.Module
			var err error

			if module, err = factory(ctx, config, logger); err != nil {
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

func TestNewServerOptionsKeepsErrorHandlersIndependent(t *testing.T) {
	firstHandler := func(_ int, _ error) any {
		return "first"
	}
	secondHandler := func(_ int, _ error) any {
		return "second"
	}

	firstOptions, err := newServerOptions(WithErrorHandler(firstHandler))
	require.NoError(t, err)

	secondOptions, err := newServerOptions(WithErrorHandler(secondHandler))
	require.NoError(t, err)

	assert.Equal(t, "first", firstOptions.errorHandler(0, errors.New("ignored")))
	assert.Equal(t, "second", secondOptions.errorHandler(0, errors.New("ignored")))
}

func TestNewServerWithSettingsUsesConfiguredErrorHandler(t *testing.T) {
	handler := func(_ int, err error) any {
		return struct {
			Error string `json:"error"`
		}{Error: err.Error()}
	}

	server := buildServerForOptionsTest(t, NewServerWithSettings(
		t.Context(),
		"test",
		serverOptionRouterFactory,
		&Settings{
			Port: "0",
			Mode: gin.TestMode,
			Errors: ErrorsSettings{
				Privacy: ErrorPrivacyPublic,
			},
			Compression: CompressionSettings{
				Level: "none",
			},
		},
		WithErrorHandler(handler),
	))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/error", http.NoBody)
	request.Header.Set(HeaderAccept, ContentTypeApplicationJson)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, ContentTypeJson, recorder.Header().Get(HeaderContentType))
	assert.JSONEq(t, `{"error":"server error"}`, recorder.Body.String())
}
