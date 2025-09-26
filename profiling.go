package httpserver

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
)

type Profiling struct {
	kernel.BackgroundModule
	kernel.ServiceStage

	logger   log.Logger
	router   *fiber.App
	settings *ProfilingSettings
}

func ProfilingModuleFactory(_ context.Context, config cfg.Config, _ log.Logger) (map[string]kernel.ModuleFactory, error) {
	settings := &ProfilingSettings{}
	if err := config.UnmarshalKey("profiling", settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal profiling settings: %w", err)
	}

	if !settings.Enabled {
		return nil, nil
	}

	return map[string]kernel.ModuleFactory{
		"profiling": func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
			router := fiber.New()

			profiling := NewProfilingWithInterfaces(logger, router, settings)

			return profiling, nil
		},
	}, nil
}

func NewProfilingWithInterfaces(logger log.Logger, router *fiber.App, settings *ProfilingSettings) *Profiling {
	AddProfilingEndpoints(router)

	return &Profiling{
		logger:   logger,
		router:   router,
		settings: settings,
	}
}

func (p *Profiling) Run(ctx context.Context) error {
	go p.waitForStop(ctx)

	addr := fmt.Sprintf(":%d", p.settings.Api.Port)
	conf := fiber.ListenConfig{
		DisableStartupMessage: true,
	}

	if err := p.router.Listen(addr, conf); err != nil {
		return fmt.Errorf("profiling api server closed unexpected: %w", err)
	}

	return nil
}

func (p *Profiling) waitForStop(ctx context.Context) {
	<-ctx.Done()

	if err := p.router.Shutdown(); err != nil {
		p.logger.Error(ctx, "profiling api server close", err)
	}
}
