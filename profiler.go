package httpserver

import (
	"net/http"
	"net/http/pprof"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

const (
	BaseProfiling = "/debug/profiling"
	CmdLine       = "/cmdline"
	Profile       = "/profile"
	Symbol        = "/symbol"
	Trace         = "/trace"
	Allocs        = "/allocs"
	Block         = "/block"
	GoRoutine     = "/goroutine"
	Heap          = "/heap"
	Mutex         = "/mutex"
	ThreadCreate  = "/threadcreate"
)

func AddProfilingEndpoints(r *fiber.App) {
	pr := r.Group(BaseProfiling)
	pr.Get("/", adaptor.HTTPHandler(http.HandlerFunc(pprof.Index)))
	pr.Get(CmdLine, adaptor.HTTPHandler(http.HandlerFunc(pprof.Cmdline)))
	pr.Get(Profile, adaptor.HTTPHandler(http.HandlerFunc(pprof.Profile)))
	pr.Post(Symbol, adaptor.HTTPHandler(http.HandlerFunc(pprof.Symbol)))
	pr.Get(Symbol, adaptor.HTTPHandler(http.HandlerFunc(pprof.Symbol)))
	pr.Get(Trace, adaptor.HTTPHandler(http.HandlerFunc(pprof.Trace)))
	pr.Get(Allocs, adaptor.HTTPHandler(pprof.Handler("allocs")))
	pr.Get(Block, adaptor.HTTPHandler(pprof.Handler("block")))
	pr.Get(GoRoutine, adaptor.HTTPHandler(pprof.Handler("goroutine")))
	pr.Get(Heap, adaptor.HTTPHandler(pprof.Handler("heap")))
	pr.Get(Mutex, adaptor.HTTPHandler(pprof.Handler("mutex")))
	pr.Get(ThreadCreate, adaptor.HTTPHandler(pprof.Handler("threadcreate")))
}
