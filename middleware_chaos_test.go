package httpserver_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/suite"
)

const fullBody = "hello world - this is some response body content for testing"

type middlewareChaosTestSuite struct {
	suite.Suite

	logger logMocks.LoggerMock
}

func TestMiddlewareChaosTestSuite(t *testing.T) {
	suite.Run(t, new(middlewareChaosTestSuite))
}

func (s *middlewareChaosTestSuite) SetupTest() {
	gin.SetMode(gin.TestMode)

	s.logger = logMocks.NewLoggerMock(logMocks.WithTestingT(s.T()), logMocks.WithMockUntilLevel(log.PriorityWarn))
}

func (s *middlewareChaosTestSuite) newRouter(settings httpserver.ChaosSettings) *gin.Engine {
	handler := httpserver.ChaosMiddleware(s.T().Context(), s.logger, settings)

	router := gin.New()
	router.Use(handler)
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, fullBody)
	})

	return router
}

func (s *middlewareChaosTestSuite) serve(settings httpserver.ChaosSettings) (*httptest.Server, *http.Response) {
	server := httptest.NewServer(s.newRouter(settings))
	s.T().Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	s.Require().NoError(err)

	return server, resp
}

func (s *middlewareChaosTestSuite) newRequest() *http.Request {
	req, err := http.NewRequest(http.MethodGet, "/", http.NoBody)
	s.Require().NoError(err)

	return req
}

func (s *middlewareChaosTestSuite) TestDisabledPassesThrough() {
	recorder := httptest.NewRecorder()
	s.newRouter(httpserver.ChaosSettings{Enabled: false}).ServeHTTP(recorder, s.newRequest())

	s.Equal(http.StatusOK, recorder.Code)
	s.Equal(fullBody, recorder.Body.String())
}

func (s *middlewareChaosTestSuite) TestReject100Percent() {
	recorder := httptest.NewRecorder()
	s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Reject:  httpserver.ChaosRejectSettings{Percent: 100, StatusCodes: []int{503}},
	}).ServeHTTP(recorder, s.newRequest())

	s.Equal(http.StatusServiceUnavailable, recorder.Code)
}

func (s *middlewareChaosTestSuite) TestReject0PercentPassesThrough() {
	recorder := httptest.NewRecorder()
	s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Reject:  httpserver.ChaosRejectSettings{Percent: 0, StatusCodes: []int{503}},
	}).ServeHTTP(recorder, s.newRequest())

	s.Equal(http.StatusOK, recorder.Code)
}

func (s *middlewareChaosTestSuite) TestRejectUsesDefaultStatusCodes() {
	recorder := httptest.NewRecorder()
	s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Reject:  httpserver.ChaosRejectSettings{Percent: 100},
	}).ServeHTTP(recorder, s.newRequest())

	s.Contains([]int{499, 500, 502, 503, 504}, recorder.Code)
}

func (s *middlewareChaosTestSuite) TestRejectDistribution() {
	router := s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Reject:  httpserver.ChaosRejectSettings{Percent: 50, StatusCodes: []int{503}},
	})

	rejected := 0
	for range 1000 {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, s.newRequest())

		if recorder.Code == http.StatusServiceUnavailable {
			rejected++
		}
	}

	s.Greater(rejected, 300, "too few rejections for 50%% rate")
	s.Less(rejected, 700, "too many rejections for 50%% rate")
}

func (s *middlewareChaosTestSuite) TestDelay100PercentAddsLatency() {
	recorder := httptest.NewRecorder()

	start := time.Now()
	s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Delay:   httpserver.ChaosDelaySettings{Percent: 100, MinDuration: 30 * time.Millisecond, MaxDuration: 50 * time.Millisecond},
	}).ServeHTTP(recorder, s.newRequest())
	elapsed := time.Since(start)

	s.Equal(http.StatusOK, recorder.Code)
	s.Equal(fullBody, recorder.Body.String())
	s.GreaterOrEqual(elapsed, 30*time.Millisecond, "delay should be at least MinDuration")
}

func (s *middlewareChaosTestSuite) TestDelayRespectsContextCancellation() {
	router := s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Delay:   httpserver.ChaosDelaySettings{Percent: 100, MinDuration: 5 * time.Second, MaxDuration: 5 * time.Second},
	})

	ctx, cancel := context.WithCancel(s.T().Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	s.Require().NoError(err)

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		s.Fail("chaos delay did not respect context cancellation")
	}
}

func (s *middlewareChaosTestSuite) TestDrop100PercentClosesConnection() {
	var response *http.Response
	var err error

	server := httptest.NewServer(s.newRouter(httpserver.ChaosSettings{
		Enabled: true,
		Drop:    httpserver.ChaosDropSettings{Percent: 100},
	}))
	defer server.Close()

	if response, err = http.Get(server.URL + "/"); err != nil {
		s.Contains(err.Error(), "EOF", "expected EOF or connection error, got: %v", err)

		return
	}
	defer func() { s.NoError(response.Body.Close()) }()

	body, err := io.ReadAll(response.Body)
	s.Require().NoError(err)
	s.Empty(body, "expected no response body on drop")
}

func (s *middlewareChaosTestSuite) TestDrop0PercentPassesThrough() {
	_, response := s.serve(httpserver.ChaosSettings{
		Enabled: true,
		Drop:    httpserver.ChaosDropSettings{Percent: 0},
	})
	defer func() { s.NoError(response.Body.Close()) }()

	body, err := io.ReadAll(response.Body)
	s.Require().NoError(err)
	s.Equal(http.StatusOK, response.StatusCode)
	s.Equal(fullBody, string(body))
}

func (s *middlewareChaosTestSuite) TestSlowResponse100PercentTricklesBytes() {
	server := httptest.NewServer(s.newRouter(httpserver.ChaosSettings{
		Enabled:      true,
		SlowResponse: httpserver.ChaosSlowResponseSettings{Percent: 100, Delay: 5 * time.Millisecond, ChunkSize: 5},
	}))
	defer server.Close()

	start := time.Now()
	resp, err := http.Get(server.URL + "/")
	s.Require().NoError(err)
	defer func() { s.NoError(resp.Body.Close()) }()

	body, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	s.Require().NoError(err)

	s.Equal(fullBody, string(body))
	s.Equal(http.StatusOK, resp.StatusCode)
	s.Greater(elapsed, 20*time.Millisecond, "slow response should introduce delays")
}

func (s *middlewareChaosTestSuite) TestSlowResponse0PercentPassesThrough() {
	start := time.Now()
	_, response := s.serve(httpserver.ChaosSettings{
		Enabled:      true,
		SlowResponse: httpserver.ChaosSlowResponseSettings{Percent: 0, Delay: 1 * time.Second, ChunkSize: 1},
	})
	defer func() { s.NoError(response.Body.Close()) }()

	body, err := io.ReadAll(response.Body)
	elapsed := time.Since(start)
	s.Require().NoError(err)

	s.Equal(fullBody, string(body))
	s.Less(elapsed, 500*time.Millisecond, "0%% slow response should not introduce delays")
}

func (s *middlewareChaosTestSuite) TestTruncate100PercentSendsPartialBody() {
	server := httptest.NewServer(s.newRouter(httpserver.ChaosSettings{
		Enabled:  true,
		Truncate: httpserver.ChaosTruncateSettings{Percent: 100, MaxBytes: 10},
	}))
	defer server.Close()

	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 2*time.Second)
	s.Require().NoError(err)

	_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	s.Require().NoError(err)

	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)))
	// io.ReadAll may return an error (EOF, connection reset, or timeout) because
	// the server forcibly closes the connection after partial write. The data read
	// before the error is still valid and is what we want to assert on.
	raw, readErr := io.ReadAll(conn)
	s.Error(readErr)

	response := string(raw)
	s.Contains(response, "HTTP/1.1 200", "should receive status line")

	s.NotContains(response, fullBody, "full body should NOT be present in truncated response")
}

func (s *middlewareChaosTestSuite) TestTruncate0PercentPassesThrough() {
	_, response := s.serve(httpserver.ChaosSettings{
		Enabled:  true,
		Truncate: httpserver.ChaosTruncateSettings{Percent: 0, MaxBytes: 5},
	})
	defer func() { s.NoError(response.Body.Close()) }()

	body, err := io.ReadAll(response.Body)
	s.Require().NoError(err)

	s.Equal(http.StatusOK, response.StatusCode)
	s.Equal(fullBody, string(body))
}
