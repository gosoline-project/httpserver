package httpserver

import (
	"time"

	"github.com/justtrackio/gosoline/pkg/application"
	"github.com/justtrackio/gosoline/pkg/exec"
)

// RunDefaultServer starts an application with a single HTTP server named "default".
func RunDefaultServer(routerFactory RouterFactory, options ...application.Option) {
	RunServers(
		map[string]ServerDefinition{
			"default": {
				RouterFactory: routerFactory,
				Options:       nil,
			},
		},
		options...,
	)
}

// RunServerWithOptions starts an application with one HTTP server named "default"
// and configures it with serverOptions in addition to application options.
func RunServerWithOptions(routerFactory RouterFactory, serverOptions []ServerOption, options ...application.Option) {
	RunServers(
		map[string]ServerDefinition{
			"default": {
				RouterFactory: routerFactory,
				Options:       serverOptions,
			},
		},
		options...,
	)
}

// RunServers starts an application with one HTTP server module per provided server definition.
func RunServers(servers map[string]ServerDefinition, options ...application.Option) {
	options = append(options, application.WithExecBackoffSettings(&exec.BackoffSettings{
		InitialInterval: time.Millisecond * 100,
		MaxElapsedTime:  time.Second * 10,
		MaxInterval:     time.Second,
	}))
	options = append(options, application.WithConfigFile("config.dist.yml", "yml"))

	for name, serverDefinition := range servers {
		options = append(options, application.WithModuleFactory("httpserver-"+name, NewServer(name, serverDefinition.RouterFactory, serverDefinition.Options...)))
	}

	application.Run(options...)
}

type ServerDefinition struct {
	RouterFactory RouterFactory
	Options       []ServerOption
}
