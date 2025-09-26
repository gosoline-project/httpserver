package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

type (
	RouterFactory func(ctx context.Context, config cfg.Config, logger log.Logger, router *Router) error
	RouterGroup   struct {
		path   string
		router *Router
	}
)

type Router struct {
	app               fiber.Router
	groups            []RouterGroup
	registerFactories []registerFactoryFunc
}

func NewRouter(app fiber.Router) *Router {
	return &Router{
		app: app,
	}
}

func (r *Router) Build(ctx context.Context, config cfg.Config, logger log.Logger) (fiber.Router, error) {
	for _, registerFactory := range r.registerFactories {
		register, err := registerFactory(ctx, config, logger, r)

		if err != nil {
			return nil, err
		}

		register(r)
	}

	for _, group := range r.groups {
		if _, err := group.router.Build(ctx, config, logger); err != nil {
			return nil, fmt.Errorf("can not build router for group %q: %w", group.path, err)
		}
	}

	return r.app, nil
}

func (r *Router) Group(path string, handlers ...fiber.Handler) *Router {
	grp := r.app.Group(path, handlers...)
	grpRouter := NewRouter(grp)

	r.groups = append(r.groups, RouterGroup{
		path:   path,
		router: grpRouter,
	})

	return grpRouter
}

func (r *Router) Handle(method string, path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.app.Add([]string{method}, path, handler, handlers...)

	return r
}

func (r *Router) HandleWith(registerFactory registerFactoryFunc) {
	r.registerFactories = append(r.registerFactories, registerFactory)
}

func (r *Router) Use(middlewares ...fiber.Handler) *Router {
	anySl := make([]any, len(middlewares))
	for i, m := range middlewares {
		anySl[i] = m
	}

	r.app.Use(anySl...)

	return r
}

func (r *Router) Delete(path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.Handle(http.MethodDelete, path, handler)

	return r
}

func (r *Router) Get(path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.Handle(http.MethodGet, path, handler)

	return r
}

func (r *Router) Patch(path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.Handle(http.MethodPatch, path, handler)

	return r
}

func (r *Router) Post(path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.Handle(http.MethodPost, path, handler)

	return r
}

func (r *Router) Put(path string, handler fiber.Handler, handlers ...fiber.Handler) *Router {
	r.Handle(http.MethodPut, path, handler)

	return r
}
