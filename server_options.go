package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ServerOption configures a server during construction.
//
// Errors returned by an option prevent the server module from being created.
type ServerOption func(*serverOptions) error

type serverOptions struct {
	responseNegotiator ResponseNegotiator
	errorMappers       []ErrorMapper
	errorHandler       ErrorHandler
	websocket          WebsocketOptions
}

// WithResponseNegotiator configures the response negotiator used by the server.
func WithResponseNegotiator(negotiator ResponseNegotiator) ServerOption {
	return func(options *serverOptions) error {
		if negotiator == nil {
			return errors.New("response negotiator is required")
		}

		options.responseNegotiator = negotiator

		return nil
	}
}

// WithErrorHandler configures the error body handler used by the server.
func WithErrorHandler(handler ErrorHandler) ServerOption {
	return func(options *serverOptions) error {
		if handler == nil {
			return errors.New("error handler is required")
		}

		options.errorHandler = handler

		return nil
	}
}

// WithErrorMapper configures an application error mapper used by the server.
func WithErrorMapper(mapper ErrorMapper) ServerOption {
	return func(options *serverOptions) error {
		if mapper == nil {
			return errors.New("error mapper is required")
		}

		options.errorMappers = append(options.errorMappers, mapper)

		return nil
	}
}

// WebsocketOptions configures managed WebSocket connections. Zero numeric values
// select the defaults; a nil CheckOrigin uses same-host or absent-Origin checks.
type WebsocketOptions struct {
	ReadLimit    int64
	WriteTimeout time.Duration
	CloseTimeout time.Duration
	CheckOrigin  func(*http.Request) bool
}

func defaultWebsocketOptions() WebsocketOptions {
	return WebsocketOptions{
		ReadLimit:    1 << 20,
		WriteTimeout: 5 * time.Second,
		CloseTimeout: time.Second,
	}
}

func normalizeWebsocketOptions(options WebsocketOptions) (WebsocketOptions, error) {
	if options.ReadLimit < 0 || options.WriteTimeout < 0 || options.CloseTimeout < 0 {
		return WebsocketOptions{}, fmt.Errorf("websocket read limit, write timeout and close timeout must not be negative")
	}
	defaults := defaultWebsocketOptions()
	if options.ReadLimit == 0 {
		options.ReadLimit = defaults.ReadLimit
	}
	if options.WriteTimeout == 0 {
		options.WriteTimeout = defaults.WriteTimeout
	}
	if options.CloseTimeout == 0 {
		options.CloseTimeout = defaults.CloseTimeout
	}

	return options, nil
}

// WithWebsocketOptions configures WebSocket bounds independently of HTTP timeouts
// and CORS. Negative bounds fail server construction before opening a listener.
func WithWebsocketOptions(options WebsocketOptions) ServerOption {
	return func(server *serverOptions) error {
		var normalized WebsocketOptions
		var err error
		if normalized, err = normalizeWebsocketOptions(options); err != nil {
			return fmt.Errorf("configure websocket options: %w", err)
		}
		server.websocket = normalized

		return nil
	}
}

func newServerOptions(options ...ServerOption) (*serverOptions, error) {
	opts := &serverOptions{
		responseNegotiator: NewDefaultResponseNegotiator(),
		errorHandler:       defaultErrorHandler,
		websocket:          defaultWebsocketOptions(),
	}

	for _, option := range options {
		if option == nil {
			return nil, errors.New("server option is required")
		}

		if err := option(opts); err != nil {
			return nil, fmt.Errorf("could not apply server option: %w", err)
		}
	}

	return opts, nil
}
