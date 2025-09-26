package httpserver

import (
	"time"

	"github.com/justtrackio/gosoline/pkg/application"
	"github.com/justtrackio/gosoline/pkg/exec"
)

func RunDefaultServer(routerFactory RouterFactory, options ...application.Option) {
	RunServers(
		map[string]RouterFactory{
			"default": routerFactory,
		},
		options...,
	)
}

func RunServers(servers map[string]RouterFactory, options ...application.Option) {
	options = append(options, application.WithExecBackoffSettings(&exec.BackoffSettings{
		InitialInterval: time.Millisecond * 100,
		MaxElapsedTime:  time.Second * 10,
		MaxInterval:     time.Second,
	}))

	for name, routerFactory := range servers {
		options = append(options, application.WithModuleFactory("httpserver-"+name, NewServer(name, routerFactory)))
	}

	application.Run(options...)
}
