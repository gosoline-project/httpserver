package httpserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/kernel"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/stretchr/testify/assert"
)

func HealthCheckerMock() kernel.HealthCheckResult {
	return make(kernel.HealthCheckResult, 0)
}

func TestNewApiHealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginEngine := gin.New()
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	httpserver.NewHealthCheckWithInterfaces(logger, ginEngine, HealthCheckerMock, &httpserver.HealthCheckSettings{
		Path: "/health",
	})

	httpRecorder := httptest.NewRecorder()
	assertRouteReturnsResponse(t, ginEngine, httpRecorder, "/health", http.StatusOK)
}

func TestHealthCheck_UnhealthyModuleDoesNotExposeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginEngine := gin.New()
	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	healthChecker := func() kernel.HealthCheckResult {
		return kernel.HealthCheckResult{
			{
				Name:    "database",
				Healthy: false,
				Err:     errors.New("connection string contains secret"),
			},
		}
	}

	httpserver.NewHealthCheckWithInterfaces(logger, ginEngine, healthChecker, &httpserver.HealthCheckSettings{
		Path: "/health",
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	ginEngine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(t, `{"database":"unhealthy"}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "connection string contains secret")
}
