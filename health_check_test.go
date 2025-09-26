package httpserver_test

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/kernel"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
)

func HealthCheckerMock() kernel.HealthCheckResult {
	return make(kernel.HealthCheckResult, 0)
}

func TestNewApiHealthCheck(t *testing.T) {
	ginEngine := fiber.New()
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	httpserver.NewHealthCheckWithInterfaces(logger, ginEngine, HealthCheckerMock, &httpserver.HealthCheckSettings{
		Path: "/health",
	})

	assertRouteReturnsResponse(t, ginEngine, "/health", http.StatusOK)
}
