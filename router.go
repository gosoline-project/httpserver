package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

type (
	Handler[I any] interface {
		Handle(ctx context.Context, input *I) (Response, error)
	}
	HandlerFunc[I any] func(ctx context.Context, input *I) (Response, error)
	// RouterFactory defines routes on the provided router during server startup.
	RouterFactory func(ctx context.Context, config cfg.Config, logger log.Logger, router *Router) error
	// MiddlewareFactory creates a Gin middleware from application dependencies and server settings.
	MiddlewareFactory func(ctx context.Context, config cfg.Config, logger log.Logger, settings *Settings) (gin.HandlerFunc, error)
)

// Definition stores one route registered on a Router.
type Definition struct {
	Group        *Router
	HttpMethod   string
	RelativePath string
	Handlers     []gin.HandlerFunc
}

func (d *Definition) getAbsolutePath() string {
	groupPath := d.Group.getAbsolutePath()

	absolutePath := fmt.Sprintf("%s/%s", groupPath, d.RelativePath)
	absolutePath = trimRightPath(absolutePath)

	return removeDuplicates(absolutePath)
}

// Router stores route definitions, middleware, and nested groups before they are mounted on Gin.
type Router struct {
	basePath            string
	registerFactories   []RegisterFactoryFunc
	middlewareFactories []MiddlewareFactory
	routes              []Definition

	children []*Router
	parent   *Router
}

func (d *Router) getAbsolutePath() string {
	parentPath := "/"

	if d.parent != nil {
		parentPath = d.parent.getAbsolutePath()
	}

	absolutePath := fmt.Sprintf("%s/%s", parentPath, d.basePath)

	return removeDuplicates(absolutePath)
}

// Group creates a nested router group below the current router path.
func (d *Router) Group(relativePath string) *Router {
	newGroup := &Router{
		basePath: relativePath,
		children: make([]*Router, 0),
		parent:   d,
	}

	d.children = append(d.children, newGroup)

	return newGroup
}

// Use adds Gin middleware to the current router group.
func (d *Router) Use(middlewares ...gin.HandlerFunc) {
	for _, middleware := range middlewares {
		d.UseFactory(func(_ context.Context, _ cfg.Config, _ log.Logger, _ *Settings) (gin.HandlerFunc, error) {
			return middleware, nil
		})
	}
}

// UseFactory adds middleware factories to the current router group.
func (d *Router) UseFactory(factories ...MiddlewareFactory) {
	d.middlewareFactories = append(d.middlewareFactories, factories...)
}

// Handle registers a route for the provided HTTP method and relative path.
func (d *Router) Handle(httpMethod, relativePath string, handlers ...gin.HandlerFunc) {
	relativePath = trimRightPath(relativePath)

	d.routes = append(d.routes, Definition{
		Group:        d,
		HttpMethod:   httpMethod,
		RelativePath: relativePath,
		Handlers:     handlers,
	})
}

// HandleWith adds a registration factory, usually created with With, to the router.
func (r *Router) HandleWith(registerFactory RegisterFactoryFunc) {
	r.registerFactories = append(r.registerFactories, registerFactory)
}

// PATCH registers a PATCH route.
func (d *Router) PATCH(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodPatch, relativePath, handlers...)
}

// POST registers a POST route.
func (d *Router) POST(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodPost, relativePath, handlers...)
}

// GET registers a GET route.
func (d *Router) GET(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodGet, relativePath, handlers...)
}

// DELETE registers a DELETE route.
func (d *Router) DELETE(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodDelete, relativePath, handlers...)
}

// PUT registers a PUT route.
func (d *Router) PUT(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodPut, relativePath, handlers...)
}

// OPTIONS registers an OPTIONS route.
func (d *Router) OPTIONS(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.MethodOptions, relativePath, handlers...)
}

func buildRouter(ctx context.Context, config cfg.Config, logger log.Logger, settings *Settings, definitions *Router, router gin.IRouter) ([]Definition, error) {
	if definitions == nil {
		return nil, fmt.Errorf("route definitions should not be nil")
	}

	var err error
	var register func(router *Router)
	var definitionList, childDefinitions []Definition
	var middleware gin.HandlerFunc

	for _, registerFactory := range definitions.registerFactories {
		if register, err = registerFactory(ctx, config, logger, definitions); err != nil {
			return nil, err
		}

		register(definitions)
	}

	grp := router

	if definitions.parent != nil {
		grp = router.Group(definitions.basePath)
	}

	for _, f := range definitions.middlewareFactories {
		if middleware, err = f(ctx, config, logger, settings); err != nil {
			return nil, fmt.Errorf("error creating middleware: %w", err)
		}

		grp.Use(middleware)
	}

	for _, d := range definitions.routes {
		handlers := make([]gin.HandlerFunc, 0, len(d.Handlers)+1)
		handlers = append(handlers, d.Handlers...)

		grp.Handle(d.HttpMethod, d.RelativePath, handlers...)
	}

	definitionList = append(definitionList, definitions.routes...)
	for _, c := range definitions.children {
		if childDefinitions, err = buildRouter(ctx, config, logger, settings, c, grp); err != nil {
			return nil, fmt.Errorf("error building children: %w", err)
		}

		definitionList = append(definitionList, childDefinitions...)
	}

	return definitionList, nil
}

func removeDuplicates(s string) string {
	var buf strings.Builder
	var last rune

	for i, r := range s {
		if i == 0 || r != '/' || r != last {
			buf.WriteRune(r)
		}

		last = r
	}

	return buf.String()
}

func trimRightPath(path string) string {
	absolutePath := strings.TrimRight(path, "/")

	if absolutePath == "" {
		absolutePath = "/"
	}

	return absolutePath
}
