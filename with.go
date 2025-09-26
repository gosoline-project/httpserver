package httpserver

import (
	"context"
	"fmt"

	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

type HandlerFactory[H any] func(ctx context.Context, config cfg.Config, logger log.Logger) (*H, error)
type registerFactoryFunc func(ctx context.Context, config cfg.Config, logger log.Logger, router *Router) (func(router *Router), error)
type registerFunc[H any] func(router *Router, handler *H)

func With[H any](handlerFactory HandlerFactory[H], register registerFunc[H]) registerFactoryFunc {
	return func(ctx context.Context, config cfg.Config, logger log.Logger, router *Router) (func(router *Router), error) {
		var err error
		var handler *H

		if handler, err = handlerFactory(ctx, config, logger); err != nil {
			return nil, fmt.Errorf("failed to create handler of type %T: %w", *new(H), err)
		}

		return func(router *Router) {
			register(router, handler)
		}, nil
	}
}
