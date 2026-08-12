package httpserver_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
)

func TestHttpServerTestSuite(t *testing.T) {
	suite.Run(t, new(HttpServerTestSuite))
}

type typedHttpServerResponse struct {
	XMLName xml.Name `xml:"response" json:"-"`
	Message string   `json:"message" xml:"message"`
}

type HttpServerTestSuite struct {
	suite.Suite
}

func (s *HttpServerTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("info"),
		suite.WithSharedEnvironment(),
	}
}

func (s *HttpServerTestSuite) SetupHttpServerRouter() httpserver.RouterFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router, _ *httpserver.HttpServer) error {
		negotiator, err := httpserver.NewContentNegotiator(
			httpserver.ContentTypeApplicationJson,
			httpserver.JSONRepresentation(),
			httpserver.XMLRepresentation(),
		)
		if err != nil {
			return err
		}
		router.Use(httpserver.ResponseNegotiationMiddleware(negotiator))

		router.GET("/panic", func(ginCtx *gin.Context) {
			panic("something went wrong")
		})

		router.GET("/noop", func(ginCtx *gin.Context) {
			ginCtx.String(http.StatusOK, "{}")
		})

		router.POST("/echo", func(ginCtx *gin.Context) {
			body, err := io.ReadAll(ginCtx.Request.Body)
			s.NoError(err)

			contentType := ginCtx.ContentType()

			ginCtx.Data(http.StatusOK, contentType, body)
		})

		router.POST("/reverse", func(ginCtx *gin.Context) {
			body, err := io.ReadAll(ginCtx.Request.Body)
			s.NoError(err)

			contentType := ginCtx.ContentType()

			ginCtx.Data(http.StatusOK, contentType, funk.Reverse(body))
		})

		router.GET("/typed-response", httpserver.BindN(func(context.Context) (typedHttpServerResponse, error) {
			return typedHttpServerResponse{Message: "hello from the server"}, nil
		}))

		return nil
	}
}

func (s *HttpServerTestSuite) TestBase(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	response, err := client.R().
		SetBody("this is a test").
		Execute(http.MethodPost, "/reverse")
	if err != nil {
		return err
	}

	s.Equal(http.StatusOK, response.StatusCode())
	s.Equal(funk.Reverse([]byte("this is a test")), response.Body())

	return nil
}

func (s *HttpServerTestSuite) TestTypedResponseIsMarshaledByServer(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	var response *resty.Response
	var err error
	response, err = client.R().Get("/typed-response")
	if err != nil {
		return err
	}

	s.Equal(http.StatusOK, response.StatusCode())
	s.Equal(httpserver.ContentTypeJson, response.Header().Get(httpserver.HeaderContentType))
	s.JSONEq(`{"message":"hello from the server"}`, string(response.Body()))

	return nil
}

func (s *HttpServerTestSuite) TestTypedResponseUsesConfiguredXMLRepresentation(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	response, err := client.R().
		SetHeader(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml).
		Get("/typed-response")
	if err != nil {
		return err
	}

	s.Equal(http.StatusOK, response.StatusCode())
	s.Equal(httpserver.ContentTypeXml, response.Header().Get(httpserver.HeaderContentType))
	s.Equal(`<response><message>hello from the server</message></response>`, string(response.Body()))

	return nil
}

func (s *HttpServerTestSuite) TestTypedResponseFallsBackToJSONForUnsupportedAccept(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	response, err := client.R().
		SetHeader(httpserver.HeaderAccept, httpserver.ContentTypeTextPlain).
		Get("/typed-response")
	if err != nil {
		return err
	}

	s.Equal(http.StatusNotAcceptable, response.StatusCode())
	s.Equal(httpserver.ContentTypeJson, response.Header().Get(httpserver.HeaderContentType))
	s.Equal("Accept-Encoding, Accept", response.Header().Get(httpserver.HeaderVary))
	s.Contains(string(response.Body()), "not acceptable")

	return nil
}

func (s *HttpServerTestSuite) TestRecover(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	response, err := client.R().
		SetHeader(httpserver.HeaderAccept, httpserver.ContentTypeApplicationXml).
		SetBody("this is a test").
		Execute(http.MethodGet, "/panic")
	if err != nil {
		return err
	}

	s.Equal(http.StatusInternalServerError, response.StatusCode())
	s.Equal(httpserver.ContentTypeXml, response.Header().Get(httpserver.HeaderContentType))
	s.Equal("Accept-Encoding, Accept", response.Header().Get(httpserver.HeaderVary))
	s.Equal(`<error><err>something went wrong</err></error>`, string(response.Body()))

	return nil
}
