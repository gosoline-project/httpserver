package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/dx"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
)

func init() {
	dx.RegisterRandomizablePortSetting("httpserver.health-check.port")
}

type HttpServerHealthCheck struct {
	kernel.BackgroundModule
	kernel.EssentialStage

	logger   log.Logger
	app      *fiber.App
	settings *HealthCheckSettings
}

func NewHealthCheck() kernel.ModuleFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
		settings := &HealthCheckSettings{}
		if err := config.UnmarshalKey("httpserver.health-check", settings); err != nil {
			return nil, fmt.Errorf("failed to unmarshal health check settings: %w", err)
		}

		app := fiber.New(fiber.Config{})

		healthChecker, err := kernel.GetHealthChecker(ctx)
		if err != nil {
			return nil, fmt.Errorf("can not get health checker: %w", err)
		}

		return NewHealthCheckWithInterfaces(logger, app, healthChecker, settings), nil
	}
}

func NewHealthCheckWithInterfaces(logger log.Logger, app *fiber.App, healthChecker kernel.HealthChecker, settings *HealthCheckSettings) *HttpServerHealthCheck {
	logger = logger.WithChannel("httpserver-health-check")

	app.Use(LoggingMiddleware(logger, LoggingSettings{}))
	app.Get(settings.Path, buildHealthCheckHandler(logger, healthChecker))

	return &HttpServerHealthCheck{
		logger:   logger,
		app:      app,
		settings: settings,
	}
}

func (a *HttpServerHealthCheck) Run(ctx context.Context) error {
	go a.waitForStop(ctx)

	addr := fmt.Sprintf(":%d", a.settings.Port)
	if err := a.app.Listen(addr); err != nil {
		return fmt.Errorf("server closed unexpected: %w", err)
	}

	a.logger.Info(ctx, "leaving httpserver health check")

	return nil
}

func (s *HttpServerHealthCheck) waitForStop(ctx context.Context) {
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.settings.Timeout.Shutdown)
	defer cancel()

	s.logger.Info(ctx, "trying to gracefully shutdown httpserver health check")

	if err := s.app.ShutdownWithContext(shutdownCtx); err != nil {
		s.logger.Error(ctx, "server shutdown: %w", err)
	}
}

func buildHealthCheckHandler(logger log.Logger, healthChecker kernel.HealthChecker) func(reqCtx fiber.Ctx) error {
	return func(reqCtx fiber.Ctx) error {
		result := healthChecker()

		if result.IsHealthy() {
			reqCtx.Status(http.StatusOK)
			reqCtx.JSON(map[string]any{})

			return nil
		}

		if result.Err() != nil {
			ctx := reqCtx.Context()
			logger.Error(ctx, "encountered an error during the health check: %w", result.Err())
		}

		resp := map[string]any{}
		for _, module := range result.GetUnhealthy() {
			if module.Err != nil {
				resp[module.Name] = module.Err.Error()
			} else {
				resp[module.Name] = "unhealthy"
			}
		}

		reqCtx.Status(http.StatusInternalServerError)
		reqCtx.JSON(resp)

		return nil
	}
}
