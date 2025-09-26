package httpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/http"
	"github.com/justtrackio/gosoline/pkg/log"
)

type RouterFactory func(ctx context.Context, config cfg.Config, logger log.Logger, router *Router) error

type Definition struct {
	group        *Router
	httpMethod   string
	relativePath string
	handlers     []gin.HandlerFunc
}

func (d *Definition) getAbsolutePath() string {
	groupPath := d.group.getAbsolutePath()

	absolutePath := fmt.Sprintf("%s/%s", groupPath, d.relativePath)
	absolutePath = trimRightPath(absolutePath)

	return removeDuplicates(absolutePath)
}

type Router struct {
	basePath          string
	registerFactories []registerFactoryFunc
	middleware        []gin.HandlerFunc
	routes            []Definition

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

func (d *Router) Group(relativePath string) *Router {
	newGroup := &Router{
		basePath: relativePath,
		children: make([]*Router, 0),
		parent:   d,
	}

	d.children = append(d.children, newGroup)

	return newGroup
}

func (d *Router) Use(middleware ...gin.HandlerFunc) {
	d.middleware = append(d.middleware, middleware...)
}

func (d *Router) Handle(httpMethod, relativePath string, handlers ...gin.HandlerFunc) {
	relativePath = trimRightPath(relativePath)

	d.routes = append(d.routes, Definition{
		group:        d,
		httpMethod:   httpMethod,
		relativePath: relativePath,
		handlers:     handlers,
	})
}

func (r *Router) HandleWith(registerFactory registerFactoryFunc) {
	r.registerFactories = append(r.registerFactories, registerFactory)
}

func (d *Router) PATCH(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.PatchRequest, relativePath, handlers...)
}

func (d *Router) POST(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.PostRequest, relativePath, handlers...)
}

func (d *Router) GET(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.GetRequest, relativePath, handlers...)
}

func (d *Router) DELETE(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.DeleteRequest, relativePath, handlers...)
}

func (d *Router) PUT(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.PutRequest, relativePath, handlers...)
}

func (d *Router) OPTIONS(relativePath string, handlers ...gin.HandlerFunc) {
	d.Handle(http.OptionsRequest, relativePath, handlers...)
}

func buildRouter(ctx context.Context, config cfg.Config, logger log.Logger, definitions *Router, router gin.IRouter) ([]Definition, error) {
	if definitions == nil {
		return nil, fmt.Errorf("route definitions should not be nil")
	}

	for _, registerFactory := range definitions.registerFactories {
		register, err := registerFactory(ctx, config, logger, definitions)

		if err != nil {
			return nil, err
		}

		register(definitions)
	}

	var definitionList []Definition
	grp := router

	if definitions.parent != nil {
		grp = router.Group(definitions.basePath)
	}

	for _, m := range definitions.middleware {
		grp.Use(m)
	}

	for _, d := range definitions.routes {
		handlers := make([]gin.HandlerFunc, 0, len(d.handlers)+1)
		handlers = append(handlers, d.handlers...)

		grp.Handle(d.httpMethod, d.relativePath, handlers...)
	}

	definitionList = append(definitionList, definitions.routes...)

	var err error
	var childDefinitions []Definition
	for _, c := range definitions.children {
		if childDefinitions, err = buildRouter(ctx, config, logger, c, grp); err != nil {
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
