package httpserver_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ServerTestSuite struct {
	suite.Suite
	logger log.Logger
	router *fiber.App
	server *httpserver.HttpServer
}

func TestServerTestSuite(t *testing.T) {
	suite.Run(t, new(ServerTestSuite))
}

func (s *ServerTestSuite) SetupTest() {
	s.logger = logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(s.T()))
	s.router = fiber.New()

	server, err := httpserver.NewServerWithInterfaces(s.T().Context(), s.logger, s.router, &httpserver.Settings{})
	s.NoError(err)

	s.server = server
}

func (s *ServerTestSuite) TestLifecycle_Cancel() {
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	s.NotPanics(func() {
		err := s.server.Run(ctx)
		s.NoError(err)
	})
}

func (s *ServerTestSuite) TestGetPort() {
	s.NotPanics(func() {
		port, err := s.server.GetPort()
		s.NoError(err)
		s.NotNil(port)

		address := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
		_, err = net.Dial("tcp", address)
		s.NoError(err, "could not establish a connection with server")
	})
}

func (s *ServerTestSuite) TestBaseProfilingEndpoint() {
	s.NotPanics(func() {
		httpserver.AddProfilingEndpoints(s.router)
	})

	assertRouteReturnsResponse(s.T(), s.router, httpserver.BaseProfiling+"/", http.StatusOK)
}

func assertRouteReturnsResponse(t *testing.T, router *fiber.App, route string, responseCode int) {
	var req *http.Request
	var err error

	req, err = http.NewRequest(http.MethodGet, route, http.NoBody)
	assert.NoError(t, err, "could not create request for route %s", route)

	resp, err := router.Test(req)

	assert.NoError(t, err, "could not test request for route %s", route)
	assert.Equal(t, responseCode, resp.StatusCode)
}
