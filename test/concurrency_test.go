package test

import (
	"context"
	"fmt"
	netHttp "net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	moduleHttpserver "github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/conc"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/suite"
)

type ConcurrencyTestSuite struct {
	suite.Suite

	enteredHandler conc.SignalOnce
	releaseHandler conc.SignalOnce
}

func (s *ConcurrencyTestSuite) SetupSuite() []suite.Option {
	return []suite.Option{
		suite.WithLogLevel("debug"),
		suite.WithConfigFile("./config.dist.concurrency.yml"),
		suite.WithSharedEnvironment(),
		suite.WithoutAutoDetectedComponents("localstack"),
	}
}

func (s *ConcurrencyTestSuite) SetupTest() error {
	s.enteredHandler = conc.NewSignalOnce()
	s.releaseHandler = conc.NewSignalOnce()

	return nil
}

func (s *ConcurrencyTestSuite) SetupHttpServerRouter() moduleHttpserver.RouterFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *moduleHttpserver.Router) error {
		router.GET("/block", func(c *gin.Context) {
			s.enteredHandler.Signal()

			<-s.releaseHandler.Channel()
			c.Status(netHttp.StatusNoContent)
		})
		router.GET("/ok", func(c *gin.Context) {
			c.Status(netHttp.StatusNoContent)
		})

		return nil
	}
}

func (s *ConcurrencyTestSuite) TestRejectsRequestWhenConcurrentRequestLimitIsReached(_ suite.AppUnderTest, client *resty.Client) error {
	firstDone := make(chan error, 1)
	go func() {
		var err error
		var res *resty.Response

		if res, err = client.R().Get("/block"); err != nil {
			firstDone <- err

			return
		}

		if res.StatusCode() != netHttp.StatusNoContent {
			firstDone <- fmt.Errorf("expected first request status %d, got %d", netHttp.StatusNoContent, res.StatusCode())

			return
		}

		firstDone <- nil
	}()

	s.Require().Eventually(s.enteredHandler.Signaled, time.Second, time.Millisecond)

	res, err := client.R().Get("/ok")
	s.Require().NoError(err)
	s.Equal(netHttp.StatusTooManyRequests, res.StatusCode())
	s.Equal("1", res.Header().Get("Retry-After"))
	s.JSONEq(`{"error":"server overloaded"}`, string(res.Body()))

	s.releaseHandler.Signal()
	s.NoError(<-firstDone)

	return nil
}

func TestConcurrencyTestSuite(t *testing.T) {
	suite.Run(t, &ConcurrencyTestSuite{})
}
