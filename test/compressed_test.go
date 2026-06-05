package test

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin/binding"
	httpHeaders "github.com/go-http-utils/headers"
	"github.com/go-resty/resty/v2"
	moduleHttpserver "github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
)

type CompressedTestSuite struct {
	suite.Suite
}

func (s *CompressedTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("debug"),
		suite.WithConfigFile("./config.dist.compressed.yml"),
		suite.WithSharedEnvironment(),
		suite.WithoutAutoDetectedComponents("localstack"),
	}
}

func (s *CompressedTestSuite) SetupHttpServerRouter() moduleHttpserver.RouterFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *moduleHttpserver.Router) error {
		handler := moduleHttpserver.Bind(func(ctx context.Context, input *map[string]any) (moduleHttpserver.Response, error) {
			return moduleHttpserver.NewJsonResponse(*input), nil
		}, binding.JSON)

		router.POST("/echo", handler)
		router.POST("/uncompressed", handler)
		router.POST("/this-path-uses-no-compression-to-echo", handler)

		return nil
	}
}

func (s *CompressedTestSuite) TestCompressed() []*moduleHttpserver.HttpserverTestCase {
	// language=JSON
	bodyString := `{ "id": 42, "name": "nice json request", "content": "this is a long string. this is a long string. this is a long string. this is a long string. this is a long string. this is a long string. " }`
	// language=JSON
	expectedBody := `{"content":"this is a long string. this is a long string. this is a long string. this is a long string. this is a long string. this is a long string. ","id":42,"name":"nice json request"}`

	buffer := bytes.NewBuffer([]byte{})
	writer := gzip.NewWriter(buffer)
	_, err := writer.Write([]byte(bodyString))
	s.NoError(err)
	err = writer.Close()
	s.NoError(err)

	var result []*moduleHttpserver.HttpserverTestCase

	for i, route := range []string{"/echo", "/uncompressed", "/this-path-uses-no-compression-to-echo"} {
		result = append(result, &moduleHttpserver.HttpserverTestCase{
			Method: http.MethodPost,
			Url:    route,
			Headers: map[string]string{
				httpHeaders.ContentType:     "application/json",
				httpHeaders.ContentEncoding: "gzip",
				httpHeaders.AcceptEncoding:  "gzip",
			},
			Body:               buffer.Bytes(), // all routes should accept compressed requests
			ExpectedStatusCode: http.StatusOK,
			Assert: func(res *resty.Response) error {
				// only first route should be compressed
				if i == 0 {
					s.Equal([]string{"gzip"}, res.Header()[httpHeaders.ContentEncoding])
				} else {
					s.Equal([]string(nil), res.Header()[httpHeaders.ContentEncoding])
				}

				s.Equal([]string{"application/json; charset=utf-8"}, res.Header()[httpHeaders.ContentType])
				s.Equal(expectedBody, string(res.Body()))

				return nil
			},
		})
	}

	return result
}

func TestCompressedTestSuite(t *testing.T) {
	suite.Run(t, &CompressedTestSuite{})
}
