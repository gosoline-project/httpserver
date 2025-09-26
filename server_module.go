package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/gofiber/fiber/v3"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/justtrackio/gosoline/pkg/appctx"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/clock"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
)

type ServerMetadata struct {
	Name     string            `json:"name"`
	Handlers []HandlerMetadata `json:"handlers"`
}

// HandlerMetadata stores the Path and Method of this Handler.
type HandlerMetadata struct {
	// Method is the route method of this Handler.
	Method string `json:"method"`
	// Path is the route path ot this handler.
	Path string `json:"path"`
}

type HttpServer struct {
	kernel.EssentialModule
	kernel.ServiceStage

	logger   log.Logger
	server   *fiber.App
	listener net.Listener
	settings *Settings
	healthy  atomic.Bool
}

func NewServer(name string, routerFactory RouterFactory) kernel.ModuleFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
		settings := &Settings{}
		if err := config.UnmarshalKey(HttpserverSettingsKey(name), settings); err != nil {
			return nil, fmt.Errorf("failed to unmarshal httpserver settings: %w", err)
		}

		return NewServerWithSettings(name, routerFactory, settings)(ctx, config, logger)
	}
}

func NewServerWithSettings(name string, routerFactory RouterFactory, settings *Settings) kernel.ModuleFactory {
	return func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
		channel := fmt.Sprintf("httpserver-%s", name)
		logger = logger.WithChannel(channel)

		var (
			err error
			//compressionMiddlewares         []gin.HandlerFunc
			healthChecker                  kernel.HealthChecker
			connectionLifeCycleInterceptor fiber.Handler
		)

		metricMiddleware, setupMetricMiddleware := NewMetricMiddleware(name)

		//if compressionMiddlewares, err = configureCompression(settings.Compression); err != nil {
		//	return nil, fmt.Errorf("could not configure compression: %w", err)
		//}
		//
		if connectionLifeCycleInterceptor, err = ProvideConnectionLifeCycleInterceptor(ctx, config, logger, name); err != nil {
			return nil, fmt.Errorf("could not provide connection life cycle interceptor: %w", err)
		}

		app := fiber.New(fiber.Config{
			ProxyHeader:  fiber.HeaderXForwardedFor,
			ReadTimeout:  settings.Timeout.Read,
			WriteTimeout: settings.Timeout.Write,
			IdleTimeout:  settings.Timeout.Idle,
			ErrorHandler: func(ctx fiber.Ctx, err error) error {
				ctx.Status(http.StatusInternalServerError).JSON(fiber.Map{
					"err": err.Error(),
				})

				return nil
			},
		})

		router := NewRouter(app)
		router.Use(metricMiddleware)
		router.Use(LoggingMiddleware(logger, settings.Logging))
		//router.Use(compressionMiddlewares...)
		router.Use(recoverer.New())
		router.Use(connectionLifeCycleInterceptor)

		if healthChecker, err = kernel.GetHealthChecker(ctx); err != nil {
			return nil, fmt.Errorf("can not get health checker: %w", err)
		}
		router.Get("/health", buildHealthCheckHandler(logger, healthChecker))

		if err = routerFactory(ctx, config, logger, router); err != nil {
			return nil, fmt.Errorf("can not create router from factory: %w", err)
		}

		if _, err = router.Build(ctx, config, logger); err != nil {
			return nil, fmt.Errorf("could not build router: %w", err)
		}

		if err = appendMetadata(ctx, name, app); err != nil {
			return nil, fmt.Errorf("can not append metadata: %w", err)
		}

		setupMetricMiddleware(app)

		return NewServerWithInterfaces(ctx, logger, app, settings)
	}
}

func NewServerWithInterfaces(ctx context.Context, logger log.Logger, app *fiber.App, settings *Settings) (*HttpServer, error) {
	var err error
	var listener net.Listener
	address := ":" + settings.Port

	if address == "" {
		address = ":http"
	}

	// open a port for the server already in this step so we can already start accepting connections
	// when this module is later run (see also issue #201)
	if listener, err = net.Listen("tcp", address); err != nil {
		return nil, err
	}

	logger.Info(ctx, "serving httpserver requests on address %s", listener.Addr().String())

	apiServer := &HttpServer{
		logger:   logger,
		server:   app,
		listener: listener,
		settings: settings,
	}

	return apiServer, nil
}

func (s *HttpServer) IsHealthy(ctx context.Context) (bool, error) {
	return s.healthy.Load(), nil
}

func (s *HttpServer) Run(ctx context.Context) error {
	go s.waitForStop(ctx)

	s.server.Hooks().OnListen(func(data fiber.ListenData) error {
		s.healthy.Store(true)

		return nil
	})
	s.server.Hooks().OnPreShutdown(func() error {
		s.healthy.Store(false)

		return nil
	})

	listenerConf := fiber.ListenConfig{
		DisableStartupMessage: true,
	}

	if err := s.server.Listener(s.listener, listenerConf); err != nil {
		return fmt.Errorf("server closed unexpected: %w", err)
	}

	s.logger.Info(ctx, "leaving httpserver")

	return nil
}

func (s *HttpServer) waitForStop(ctx context.Context) {
	<-ctx.Done()

	s.logger.Info(ctx, "waiting %s until shutting down the server", s.settings.Timeout.Drain)

	t := clock.NewRealTimer(s.settings.Timeout.Drain)
	defer t.Stop()
	<-t.Chan()

	s.logger.Info(ctx, "trying to gracefully shutdown httpserver")

	if err := s.server.ShutdownWithTimeout(s.settings.Timeout.Shutdown); err != nil {
		s.logger.Error(ctx, "server shutdown: %w", err)
	}
}

func (s *HttpServer) GetPort() (*int, error) {
	if s == nil {
		return nil, errors.New("httpserver is nil, module is not yet running")
	}

	if s.listener == nil {
		return nil, errors.New("could not get port. module is not yet running")
	}

	address := s.listener.Addr().String()
	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("could not get port from address %s: %w", address, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("can not convert port string to int: %w", err)
	}

	return &port, nil
}

func appendMetadata(ctx context.Context, name string, app *fiber.App) error {
	var err error
	var metadata *appctx.Metadata

	serverMetadata := ServerMetadata{
		Name: name,
	}

	routes := app.GetRoutes()
	slices.SortFunc(routes, func(a, b fiber.Route) int {
		return strings.Compare(a.Path+a.Method, b.Path+a.Method)
	})

	for _, route := range routes {
		serverMetadata.Handlers = append(serverMetadata.Handlers, HandlerMetadata{
			Method: route.Method,
			Path:   route.Path,
		})
	}

	if metadata, err = appctx.ProvideMetadata(ctx); err != nil {
		return fmt.Errorf("can not access appctx metadata: %w", err)
	}

	if err = metadata.Append("httpservers", serverMetadata); err != nil {
		return fmt.Errorf("can not append httpserver routes to appctx metadata: %w", err)
	}

	return nil
}
