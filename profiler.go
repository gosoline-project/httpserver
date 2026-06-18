package httpserver

import (
	"net/http"
	"net/http/pprof"

	"github.com/gin-gonic/gin"
)

const (
	// BaseProfiling is the base path for profiling endpoints.
	BaseProfiling = "/debug/profiling"
	// CmdLine is the pprof command-line endpoint path.
	CmdLine = "/cmdline"
	// Profile is the pprof CPU profile endpoint path.
	Profile = "/profile"
	// Symbol is the pprof symbol endpoint path.
	Symbol = "/symbol"
	// Trace is the pprof trace endpoint path.
	Trace = "/trace"
	// Allocs is the pprof allocations endpoint path.
	Allocs = "/allocs"
	// Block is the pprof blocking profile endpoint path.
	Block = "/block"
	// GoRoutine is the pprof goroutine profile endpoint path.
	GoRoutine = "/goroutine"
	// Heap is the pprof heap profile endpoint path.
	Heap = "/heap"
	// Mutex is the pprof mutex profile endpoint path.
	Mutex = "/mutex"
	// ThreadCreate is the pprof thread creation profile endpoint path.
	ThreadCreate = "/threadcreate"
)

// AddProfilingEndpoints registers pprof-compatible profiling endpoints on the Gin engine.
func AddProfilingEndpoints(r *gin.Engine) {
	pr := r.Group(BaseProfiling)
	pr.GET("/", profilingHandler(pprof.Index))
	pr.GET(CmdLine, profilingHandler(pprof.Cmdline))
	pr.GET(Profile, profilingHandler(pprof.Profile))
	pr.POST(Symbol, profilingHandler(pprof.Symbol))
	pr.GET(Symbol, profilingHandler(pprof.Symbol))
	pr.GET(Trace, profilingHandler(pprof.Trace))
	pr.GET(Allocs, profilingHandler(pprof.Handler("allocs").ServeHTTP))
	pr.GET(Block, profilingHandler(pprof.Handler("block").ServeHTTP))
	pr.GET(GoRoutine, profilingHandler(pprof.Handler("goroutine").ServeHTTP))
	pr.GET(Heap, profilingHandler(pprof.Handler("heap").ServeHTTP))
	pr.GET(Mutex, profilingHandler(pprof.Handler("mutex").ServeHTTP))
	pr.GET(ThreadCreate, profilingHandler(pprof.Handler("threadcreate").ServeHTTP))
}

func profilingHandler(handler http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
	}
}
