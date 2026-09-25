package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	tracingMocks "github.com/justtrackio/gosoline/pkg/tracing/mocks"
	"github.com/stretchr/testify/require"
)

type shutdownTestHandler struct {
	closeErr   error
	closeCalls atomic.Int32
	closed     chan struct{}
	closeOnce  sync.Once
}

func (h *shutdownTestHandler) Close() error {
	h.closeCalls.Add(1)
	h.closeOnce.Do(func() {
		if h.closed != nil {
			close(h.closed)
		}
	})

	return h.closeErr
}

type shutdownTestHandlerWithoutClose struct{}

type shutdownTestMetricRecorder struct{}

func (shutdownTestMetricRecorder) TrackRequestStarted(context.Context)   {}
func (shutdownTestMetricRecorder) TrackRequestCompleted(context.Context) {}
func (shutdownTestMetricRecorder) TrackConnectionOpened(context.Context) {}
func (shutdownTestMetricRecorder) TrackConnectionClosed(context.Context) {}

func (shutdownTestMetricRecorder) Run(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

func TestWithRegistersHandlerCloserOnRootRouter(t *testing.T) {
	root := &Router{}
	child := root.Group("child")
	handler := &shutdownTestHandler{}

	factory := With(
		func(context.Context, cfg.Config, log.Logger) (*shutdownTestHandler, error) {
			return handler, nil
		},
		func(*Router, *shutdownTestHandler) {},
	)

	_, err := factory(t.Context(), nil, nil, child)
	require.NoError(t, err)
	require.Equal(t, []io.Closer{handler}, root.handlerClosers)
	require.Empty(t, child.handlerClosers)
}

func TestWithIgnoresHandlerWithoutClose(t *testing.T) {
	router := &Router{}
	factory := With(
		func(context.Context, cfg.Config, log.Logger) (*shutdownTestHandlerWithoutClose, error) {
			return &shutdownTestHandlerWithoutClose{}, nil
		},
		func(*Router, *shutdownTestHandlerWithoutClose) {},
	)

	_, err := factory(t.Context(), nil, nil, router)
	require.NoError(t, err)
	require.Empty(t, router.handlerClosers)
}

func TestHttpServerRunClosesHandlersAfterRequestsDrain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	router := gin.New()
	router.GET("/", func(ctx *gin.Context) {
		close(requestStarted)
		<-releaseRequest
		ctx.Status(http.StatusNoContent)
	})

	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	tracer := tracingMocks.NewInstrumentor(t)
	tracer.EXPECT().HttpHandler(router).Return(router)
	closer := &shutdownTestHandler{closed: make(chan struct{})}
	settings := &Settings{Port: "0"}
	settings.Timeout.Shutdown = time.Second

	server, err := newWithInterfaces(
		t.Context(),
		logger,
		router,
		tracer,
		settings,
		shutdownTestMetricRecorder{},
		[]io.Closer{closer},
	)
	require.NoError(t, err)

	port, err := server.GetPort()
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() {
		runDone <- server.Run(runCtx)
	}()

	responseDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", *port)) //nolint:gosec,noctx // Local loopback request exercises shutdown.
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		responseDone <- requestErr
	}()

	<-requestStarted
	require.True(t, server.healthy.Load())
	cancel()
	require.Eventually(t, func() bool {
		return !server.healthy.Load()
	}, time.Second, time.Millisecond)

	select {
	case <-closer.closed:
		t.Fatal("handler closed before the active request drained")
	default:
	}

	close(releaseRequest)
	require.NoError(t, <-responseDone)
	require.NoError(t, <-runDone)
	require.EqualValues(t, 1, closer.closeCalls.Load())
}

func TestHttpServerCloseHandlersContinuesAfterErrors(t *testing.T) {
	firstErr := errors.New("first close failed")
	first := &shutdownTestHandler{closeErr: firstErr}
	second := &shutdownTestHandler{}
	server := &HttpServer{handlerClosers: []io.Closer{first, second}}

	err := server.closeHandlers()

	require.ErrorIs(t, err, firstErr)
	require.EqualValues(t, 1, first.closeCalls.Load())
	require.EqualValues(t, 1, second.closeCalls.Load())
}
