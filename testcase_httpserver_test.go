package httpserver_test

import (
	"context"
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
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
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

func (s *HttpServerTestSuite) TestRecover(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	response, err := client.R().
		SetBody("this is a test").
		Execute(http.MethodGet, "/panic")
	if err != nil {
		return err
	}

	s.Equal(http.StatusInternalServerError, response.StatusCode())
	s.JSONEq(`{"err":"something went wrong"}`, string(response.Body()))

	return nil
}
