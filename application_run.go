package httpserver

import (
	"time"

	"github.com/justtrackio/gosoline/pkg/application"
	"github.com/justtrackio/gosoline/pkg/exec"
)

// RunDefaultServer starts an application with a single HTTP server named "default".
func RunDefaultServer(routerFactory RouterFactory, options ...application.Option) {
	RunServers(
		map[string]RouterFactory{
			"default": routerFactory,
		},
		options...,
	)
}

// RunServers starts an application with one HTTP server module per provided router factory.
func RunServers(servers map[string]RouterFactory, options ...application.Option) {
	options = append(options, application.WithExecBackoffSettings(&exec.BackoffSettings{
		InitialInterval: time.Millisecond * 100,
		MaxElapsedTime:  time.Second * 10,
		MaxInterval:     time.Second,
	}))
	options = append(options, application.WithConfigFile("config.dist.yml", "yml"))

	for name, routerFactory := range servers {
		options = append(options, application.WithModuleFactory("httpserver-"+name, NewServer(name, routerFactory)))
	}

	application.Run(options...)
}
