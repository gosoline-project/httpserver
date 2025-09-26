package httpserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/gofiber/fiber/v3"
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
		router.Get("/panic", func(fibCtx fiber.Ctx) error {
			panic("something went wrong")
		})

		router.Get("/noop", func(fibCtx fiber.Ctx) error {
			fibCtx.Status(http.StatusOK)
			fibCtx.WriteString("{}")

			return nil
		})

		router.Post("/echo", func(fibCtx fiber.Ctx) error {
			contentType := fibCtx.Request().Header.Peek("Content-Type")

			fibCtx.Status(http.StatusOK)
			fibCtx.Response().Header.SetContentType(string(contentType))
			fibCtx.Write(fibCtx.Body())

			return nil
		})

		router.Post("/reverse", func(fibCtx fiber.Ctx) error {
			contentType := fibCtx.Request().Header.Peek("Content-Type")

			fibCtx.Status(http.StatusOK)
			fibCtx.Response().Header.SetContentType(string(contentType))
			fibCtx.Write(funk.Reverse(fibCtx.Body()))

			return nil
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
