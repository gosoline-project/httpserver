package httpserver_test

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/funk"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
	"github.com/justtrackio/gosoline/pkg/test/suite/testdata"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

func TestHttpServerExtendedTestSuite(t *testing.T) {
	var s HttpServerExtendedTestSuite
	suite.Run(t, &s)
	assert.Equal(t, int32(16), s.totalTests)
}

type HttpServerExtendedTestSuite struct {
	suite.Suite
	totalTests int32
}

func (s *HttpServerExtendedTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("info"),
		suite.WithSharedEnvironment(),
	}
}

func (s *HttpServerExtendedTestSuite) SetupHttpServerRouter() httpserver.RouterFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *httpserver.Router) error {
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

func (s *HttpServerExtendedTestSuite) TestCaseMap() map[string]*httpserver.HttpserverTestCase {
	return map[string]*httpserver.HttpserverTestCase{
		"AnotherSingleTest": s.createTestCase(),
		"EmptyTest":         nil,
	}
}

func (s *HttpServerExtendedTestSuite) TestCasesMap() map[string][]*httpserver.HttpserverTestCase {
	return map[string][]*httpserver.HttpserverTestCase{
		"AnotherSingleTest": {
			s.createTestCase(),
		},
		"AnotherMultipleTests": {
			s.createTestCase(),
			s.createProtobufTestCase(),
			s.createFileTestCase(),
		},
		"EmptyTest": {},
		"AnotherSkippedTest": {
			nil,
		},
	}
}

func (s *HttpServerExtendedTestSuite) TestCasesMapWithProvider() map[string]httpserver.ToHttpserverTestCaseList {
	return map[string]httpserver.ToHttpserverTestCaseList{
		"AnotherSingleTest": s.createTestCase(),
		"AnotherMultipleTests": httpserver.HttpserverTestCaseListProvider(func() []*httpserver.HttpserverTestCase {
			return []*httpserver.HttpserverTestCase{
				s.createTestCase(),
				s.createProtobufTestCase(),
				s.createFileTestCase(),
			}
		}),
		"Nil": nil,
	}
}

func (s *HttpServerExtendedTestSuite) TestCasesEmptyMap() map[string][]*httpserver.HttpserverTestCase {
	return map[string][]*httpserver.HttpserverTestCase{}
}

func (s *HttpServerExtendedTestSuite) TestSingleTest() *httpserver.HttpserverTestCase {
	return s.createTestCase()
}

func (s *HttpServerExtendedTestSuite) TestSkipped() *httpserver.HttpserverTestCase {
	return nil
}

func (s *HttpServerExtendedTestSuite) TestNilProvider() httpserver.ToHttpserverTestCaseList {
	return nil
}

func (s *HttpServerExtendedTestSuite) TestProvider() httpserver.ToHttpserverTestCaseList {
	return s.createTestCase()
}

func (s *HttpServerExtendedTestSuite) TestMultipleTests() []*httpserver.HttpserverTestCase {
	return []*httpserver.HttpserverTestCase{
		s.createTestCase(),
		s.createProtobufTestCase(),
		s.createFileTestCase(),
	}
}

func (s *HttpServerExtendedTestSuite) TestMultipleTestsWithNil() []*httpserver.HttpserverTestCase {
	return []*httpserver.HttpserverTestCase{
		s.createTestCase(),
		nil,
		s.createFileTestCase(),
	}
}

func (s *HttpServerExtendedTestSuite) createTestCase() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method:             http.MethodGet,
		Url:                "/noop",
		Headers:            map[string]string{},
		Body:               struct{}{},
		ExpectedStatusCode: http.StatusOK,
		Assert: func(response *resty.Response) error {
			// language=JSON
			expectedResponse := `{}`
			s.JSONEq(expectedResponse, string(response.Body()))
			atomic.AddInt32(&s.totalTests, 1)

			return nil
		},
	}
}

func (s *HttpServerExtendedTestSuite) createProtobufTestCase() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method:  http.MethodPost,
		Url:     "/echo",
		Headers: map[string]string{},
		Body: httpserver.EncodeBodyProtobuf(&TestInput{
			Text: "hello, world",
		}),
		ExpectedStatusCode: http.StatusOK,
		Assert: func(response *resty.Response) error {
			body := &TestInput{}
			msg := body.EmptyMessage()

			err := proto.Unmarshal(response.Body(), msg)
			s.NoError(err)

			err = body.FromMessage(msg)
			s.NoError(err)

			expectedResponse := &TestInput{
				Text: "hello, world",
			}
			s.Equal(expectedResponse, body)
			atomic.AddInt32(&s.totalTests, 1)

			return nil
		},
	}
}

func (s *HttpServerExtendedTestSuite) createFileTestCase() *httpserver.HttpserverTestCase {
	return &httpserver.HttpserverTestCase{
		Method:             http.MethodPost,
		Url:                "/echo",
		Headers:            map[string]string{},
		Body:               httpserver.ReadBodyFile("testdata/hello world.json"),
		ExpectedStatusCode: http.StatusOK,
		Assert: func(response *resty.Response) error {
			// language=JSON
			expectedResponse := `{"text": "hello, world"}`
			s.JSONEq(expectedResponse, string(response.Body()))
			atomic.AddInt32(&s.totalTests, 1)

			return nil
		},
	}
}

//go:generate protoc --go_out=.. testcase_httpserver_extended_test_input.proto
type TestInput struct {
	Text string `json:"text" binding:"required"`
}

func (p *TestInput) ToMessage() (proto.Message, error) {
	return &testdata.TestInput{
		Text: p.Text,
	}, nil
}

func (p *TestInput) EmptyMessage() proto.Message {
	return &testdata.TestInput{}
}

func (p *TestInput) FromMessage(message proto.Message) error {
	input := message.(*testdata.TestInput)
	p.Text = input.GetText()

	return nil
}
