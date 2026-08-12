package httpserver_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type negotiatedOutput struct {
	XMLName xml.Name `xml:"result" json:"-"`
	Name    string   `json:"name" xml:"name"`
}

type negotiatedHandler struct{}

func (negotiatedHandler) Handle(context.Context, *struct{}) (negotiatedOutput, error) {
	return negotiatedOutput{Name: "alice"}, nil
}

var _ httpserver.Handler[struct{}, negotiatedOutput] = negotiatedHandler{}
var _ httpserver.HandlerFunc[struct{}, negotiatedOutput] = func(context.Context, *struct{}) (negotiatedOutput, error) {
	return negotiatedOutput{}, nil
}

func TestTypedHandlerInterfaceRendersOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(negotiatedHandler{}.Handle))

	recorder := serveRequest(router, http.MethodGet, "/result", "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"name":"alice"}`, recorder.Body.String())
}

func TestTypedHandlerFuncRendersOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := httpserver.HandlerFunc[struct{}, negotiatedOutput](func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	})

	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind[struct{}, negotiatedOutput](handler))

	recorder := serveRequest(router, http.MethodGet, "/result", "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"name":"alice"}`, recorder.Body.String())
}

func TestBindRendersTypedOutputAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.BindR(func(_ context.Context, request *http.Request, _ *struct{}) (negotiatedOutput, error) {
		assert.Equal(t, http.MethodGet, request.Method)

		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Equal(t, httpserver.HeaderAccept, recorder.Header().Get(httpserver.HeaderVary))
	assert.JSONEq(t, `{"name":"alice"}`, recorder.Body.String())
}

func TestNegotiatedResponsePreservesExistingVaryHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Header(httpserver.HeaderVary, httpserver.HeaderAcceptEncoding)
		c.Next()
	})
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.BindN(func(context.Context) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", httpserver.ContentTypeApplicationJson)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "Accept-Encoding, Accept", recorder.Header().Get(httpserver.HeaderVary))
}

func TestBindNegotiatesXMLForExplicitlyConfiguredRepresentation(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", httpserver.ContentTypeApplicationXml)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeXml, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Equal(t, `<result><name>alice</name></result>`, recorder.Body.String())
}

func TestBindNegotiatesHighestQualityRepresentation(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "application/json;q=0.2, application/xml;q=0.8")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeXml, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Equal(t, `<result><name>alice</name></result>`, recorder.Body.String())
}

func TestBindReturnsNotAcceptableForUnsupportedRepresentation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(httpserver.NewDefaultResponseNegotiator()))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "text/plain")

	assert.Equal(t, http.StatusNotAcceptable, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Contains(t, recorder.Body.String(), "not acceptable")
}

func TestBindPreservesExplicitResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(httpserver.NewDefaultResponseNegotiator()))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (httpserver.Response, error) {
		return httpserver.NewTextResponse("explicit"), nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", httpserver.ContentTypeApplicationXml)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeTextPlain, recorder.Header().Get(httpserver.HeaderContentType))
	assert.Equal(t, "explicit", recorder.Body.String())
}

func TestBindRejectsXMLByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(httpserver.NewDefaultResponseNegotiator()))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", httpserver.ContentTypeApplicationXml)

	assert.Equal(t, http.StatusNotAcceptable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "not acceptable")
}

func TestBindNRWithTypedOutputAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.BindNR(func(_ context.Context, request *http.Request) (negotiatedOutput, error) {
		assert.Equal(t, "/result", request.URL.Path)

		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	assert.JSONEq(t, `{"name":"alice"}`, recorder.Body.String())
}

func TestBindNWithTypedOutputAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.BindN(func(context.Context) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeJson, recorder.Header().Get(httpserver.HeaderContentType))
	assert.JSONEq(t, `{"name":"alice"}`, recorder.Body.String())
}

func TestNegotiationCombinesMultipleAcceptHeaderFields(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequestWithAcceptValues(
		router,
		http.MethodGet,
		"/result",
		"application/json;q=0.2",
		"application/xml;q=0.8",
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeXml, recorder.Header().Get(httpserver.HeaderContentType))
}

func TestNegotiationUsesSpecificZeroQualityOverWildcard(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.JSONRepresentation(),
		httpserver.XMLRepresentation(),
	)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "*/*;q=1, application/json;q=0")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, httpserver.ContentTypeXml, recorder.Header().Get(httpserver.HeaderContentType))
}

func TestNegotiationReturnsNotAcceptableWhenAllRepresentationsHaveZeroQuality(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(httpserver.ResponseNegotiationMiddleware(httpserver.NewDefaultResponseNegotiator()))
	router.Use(httpserver.ErrorMiddleware())
	router.GET("/result", httpserver.Bind(func(context.Context, *struct{}) (negotiatedOutput, error) {
		return negotiatedOutput{Name: "alice"}, nil
	}))

	recorder := serveRequest(router, http.MethodGet, "/result", "application/json;q=0")

	assert.Equal(t, http.StatusNotAcceptable, recorder.Code)
}

func TestNewContentNegotiatorValidatesConfiguration(t *testing.T) {
	encoder := func(any) ([]byte, error) {
		return []byte("encoded"), nil
	}

	tests := []struct {
		name             string
		defaultMediaType string
		representations  []httpserver.ResponseRepresentation
	}{
		{
			name:             "no representations",
			defaultMediaType: httpserver.ContentTypeApplicationJson,
		},
		{
			name:             "invalid default wildcard",
			defaultMediaType: "*/*",
			representations: []httpserver.ResponseRepresentation{{
				MediaType: httpserver.ContentTypeApplicationJson,
				Encode:    encoder,
			}},
		},
		{
			name:             "default is not configured",
			defaultMediaType: httpserver.ContentTypeApplicationXml,
			representations: []httpserver.ResponseRepresentation{{
				MediaType: httpserver.ContentTypeApplicationJson,
				Encode:    encoder,
			}},
		},
		{
			name:             "representation wildcard",
			defaultMediaType: httpserver.ContentTypeApplicationJson,
			representations: []httpserver.ResponseRepresentation{{
				MediaType: "application/*",
				Encode:    encoder,
			}},
		},
		{
			name:             "missing encoder",
			defaultMediaType: httpserver.ContentTypeApplicationJson,
			representations: []httpserver.ResponseRepresentation{{
				MediaType: httpserver.ContentTypeApplicationJson,
			}},
		},
		{
			name:             "duplicate representation",
			defaultMediaType: httpserver.ContentTypeApplicationJson,
			representations: []httpserver.ResponseRepresentation{
				{MediaType: httpserver.ContentTypeApplicationJson, Encode: encoder},
				{MediaType: httpserver.ContentTypeApplicationJson, Encode: encoder},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := httpserver.NewContentNegotiator(test.defaultMediaType, test.representations...)
			require.Error(t, err)
		})
	}
}

func TestContentNegotiatorReturnsEncoderError(t *testing.T) {
	expectedError := errors.New("cannot encode result")
	negotiator, err := httpserver.NewContentNegotiator(
		"application/problem+json",
		httpserver.ResponseRepresentation{
			MediaType: "application/problem+json",
			Encode: func(any) ([]byte, error) {
				return nil, expectedError
			},
		},
	)
	require.NoError(t, err)

	response, err := negotiator.Render(httptest.NewRequest(http.MethodGet, "/result", http.NoBody), struct{}{})

	require.ErrorIs(t, err, expectedError)
	assert.Nil(t, response)
}

func TestXMLRepresentationRejectsMapValues(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationXml,
		httpserver.XMLRepresentation(),
	)
	require.NoError(t, err)

	_, err = negotiator.Render(
		httptest.NewRequest(http.MethodGet, "/result", http.NoBody),
		map[string]any{"name": "alice"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestNegotiationUsesMostSpecificMediaRange(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		"text/plain",
		httpserver.ResponseRepresentation{MediaType: "text/plain", Encode: encodeMediaType},
		httpserver.ResponseRepresentation{MediaType: "text/html", Encode: encodeMediaType},
		httpserver.ResponseRepresentation{MediaType: "image/jpeg", Encode: encodeMediaType},
	)
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/result", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, "text/*;q=0.3, text/html;q=0.7, */*;q=0.5")
	response, err := negotiator.Render(request, struct{}{})

	require.NoError(t, err)
	assert.Equal(t, "text/html; charset=utf-8", response.Header().Get(httpserver.HeaderContentType))
}

func TestNegotiationIgnoresMediaParameters(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		httpserver.ContentTypeApplicationJson,
		httpserver.ResponseRepresentation{MediaType: httpserver.ContentTypeApplicationJson, Encode: encodeMediaType},
		httpserver.ResponseRepresentation{MediaType: httpserver.ContentTypeApplicationXml, Encode: encodeMediaType},
	)
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/result", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, "application/xml;profile=foo;q=0.8;trace=enabled, application/json;q=0.5")
	response, err := negotiator.Render(request, struct{}{})

	require.NoError(t, err)
	assert.Equal(t, httpserver.ContentTypeXml, response.Header().Get(httpserver.HeaderContentType))
}

func TestNegotiationRejectsQvaluesOutsideRange(t *testing.T) {
	negotiator, err := httpserver.NewContentNegotiator(
		"application/json",
		httpserver.JSONRepresentation(),
	)
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/result", http.NoBody)
	request.Header.Set(httpserver.HeaderAccept, "application/json;q=1.1")
	_, err = negotiator.Render(request, struct{}{})

	var notAcceptable *httpserver.NotAcceptableError
	require.ErrorAs(t, err, &notAcceptable)
}

func encodeMediaType(value any) ([]byte, error) {
	return []byte(fmt.Sprintf("%T", value)), nil
}

func serveRequest(router http.Handler, method string, path string, accept string) *httptest.ResponseRecorder {
	return serveRequestWithAcceptValues(router, method, path, accept)
}

func serveRequestWithAcceptValues(router http.Handler, method string, path string, accepts ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	for _, accept := range accepts {
		if accept != "" {
			request.Header.Add(httpserver.HeaderAccept, accept)
		}
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}
