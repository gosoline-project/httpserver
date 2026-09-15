package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
)

var errHttpServerOptionsSentinel = errors.New("httpserver options sentinel")

func TestHttpServerOptionsTestSuite(t *testing.T) {
	suite.Run(t, new(HttpServerOptionsTestSuite))
}

type HttpServerOptionsTestSuite struct {
	suite.Suite
}

func (s *HttpServerOptionsTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("info"),
		suite.WithSharedEnvironment(),
	}
}

func (s *HttpServerOptionsTestSuite) SetupHttpServerRouter() httpserver.RouterFactory {
	return func(_ context.Context, _ cfg.Config, _ log.Logger, router *httpserver.Router) error {
		router.GET("/wrapped-sentinel-error", httpserver.BindN(func(context.Context) (struct{}, error) {
			return struct{}{}, fmt.Errorf("request failed: %w", errHttpServerOptionsSentinel)
		}))

		return nil
	}
}

func (s *HttpServerOptionsTestSuite) SetupHttpServerOptions() []httpserver.ServerOption {
	return []httpserver.ServerOption{
		httpserver.WithErrorMapper(func(err error) (int, bool) {
			if errors.Is(err, errHttpServerOptionsSentinel) {
				return http.StatusUnprocessableEntity, true
			}

			return 0, false
		}),
		httpserver.WithErrorHandler(func(_ int, err error) any {
			return struct {
				Message string `json:"message"`
			}{Message: err.Error()}
		}),
	}
}

func (s *HttpServerOptionsTestSuite) TestMappedWrappedError(app suite.AppUnderTest, client *resty.Client) error {
	defer app.WaitDone()
	defer app.Stop()

	var response *resty.Response
	var err error
	if response, err = client.R().Get("/wrapped-sentinel-error"); err != nil {
		return err
	}

	s.Equal(http.StatusUnprocessableEntity, response.StatusCode())
	s.Equal(httpserver.ContentTypeJson, response.Header().Get(httpserver.HeaderContentType))
	s.JSONEq(`{"message":"request failed: httpserver options sentinel"}`, string(response.Body()))

	return nil
}

func (s *HttpServerOptionsTestSuite) TestMappedWrappedErrorProvider() httpserver.ToHttpserverTestCaseList {
	return httpserver.HttpserverTestCaseListProvider(func() []*httpserver.HttpserverTestCase {
		return []*httpserver.HttpserverTestCase{
			{
				Method:             http.MethodGet,
				Url:                "/wrapped-sentinel-error",
				ExpectedStatusCode: http.StatusUnprocessableEntity,
				Assert: func(response *resty.Response) error {
					s.Equal(httpserver.ContentTypeJson, response.Header().Get(httpserver.HeaderContentType))
					s.JSONEq(`{"message":"request failed: httpserver options sentinel"}`, string(response.Body()))

					return nil
				},
			},
		}
	})
}
