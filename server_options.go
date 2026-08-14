package httpserver

import (
	"errors"
	"fmt"
)

// ServerOption configures a server during construction.
//
// Errors returned by an option prevent the server module from being created.
type ServerOption func(*serverOptions) error

type serverOptions struct {
	responseNegotiator ResponseNegotiator
	errorMappers       []ErrorMapper
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

func newServerOptions(options ...ServerOption) (*serverOptions, error) {
	opts := &serverOptions{
		responseNegotiator: NewDefaultResponseNegotiator(),
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
