package httpserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
)

type InputJson struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
}

type InputUri struct {
	Id int `uri:"id"`
}

type InputMixed struct {
	Id   int    `uri:"id"`
	Name string `json:"name"`
}

func TestHttpServerBindTestSuite(t *testing.T) {
	suite.Run(t, new(HttpServerBindTestSuite))
}

type HttpServerBindTestSuite struct {
	suite.Suite
}

func (s *HttpServerBindTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("info"),
		suite.WithSharedEnvironment(),
	}
}

func (s *HttpServerBindTestSuite) SetupHttpServerRouter() httpserver.RouterFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
		router.POST("/json", httpserver.Bind(func(ctx context.Context, input *InputJson) (httpserver.Response, error) {
			return httpserver.NewJsonResponse(input), nil
		}))

		router.GET("/object/:id", httpserver.Bind(func(ctx context.Context, input *InputUri) (httpserver.Response, error) {
			return httpserver.NewJsonResponse(input), nil
		}))

		router.POST("/mixed/:id", httpserver.Bind(func(ctx context.Context, input *InputMixed) (httpserver.Response, error) {
			return httpserver.NewJsonResponse(input), nil
		}))

		return nil
	}
}

func (s *HttpServerBindTestSuite) TestJson() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method: http.MethodPost,
		Url:    "/json",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body:               `{"id": 1, "name": "alice"}`,
		ExpectedStatusCode: http.StatusOK,
		ExpectedResult:     &InputJson{},
		Assert: func(response *resty.Response) error {
			s.Equal(&InputJson{1, "alice"}, response.Result())

			return nil
		},
	}
}

func (s *HttpServerBindTestSuite) TestUri() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method:             http.MethodGet,
		Url:                "/object/3",
		ExpectedStatusCode: http.StatusOK,
		ExpectedResult:     &InputUri{},
		Assert: func(response *resty.Response) error {
			s.Equal(&InputUri{3}, response.Result())

			return nil
		},
	}
}

func (s *HttpServerBindTestSuite) TestMixed() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method: http.MethodPost,
		Url:    "/mixed/3",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body:               `{"name": "alice"}`,
		ExpectedStatusCode: http.StatusOK,
		ExpectedResult:     &InputMixed{},
		Assert: func(response *resty.Response) error {
			s.Equal(&InputMixed{3, "alice"}, response.Result())

			return nil
		},
	}
}
