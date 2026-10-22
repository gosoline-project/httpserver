package httpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

func TestHttpServerRunClosesActiveWebsocketModesBeforeApplicationClosers(t *testing.T) {
	var active atomic.Int32
	started := make(chan string, 4)
	router := newWebsocketProtocolTestRouter()
	router.GET("/read", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		active.Add(1)
		defer active.Add(-1)
		started <- "read"
		_, _, err := conn.ReadMessage()

		return err
	}))
	router.GET("/write", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		active.Add(1)
		defer active.Add(-1)
		if err := conn.Send([]byte("ready")); err != nil {
			return err
		}
		started <- "write"
		<-ctx.Done()

		return ctx.Err()
	}))
	router.GET("/reject", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		active.Add(1)
		defer active.Add(-1)
		if err := conn.RejectIncomingMessages("server stream does not accept messages"); err != nil {
			return err
		}
		started <- "reject"
		<-ctx.Done()

		return ctx.Err()
	}))

	router.GET("/quiet", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		active.Add(1)
		defer active.Add(-1)
		started <- "quiet"
		if err := conn.Send(make([]byte, 8<<20)); err != nil {
			return err
		}
		<-ctx.Done()

		return ctx.Err()
	}))

	logger := newWebsocketCountingLogger(t)
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger, func(server *HttpServer) {
		options := defaultWebsocketOptions()
		options.CloseTimeout = 500 * time.Millisecond
		server.websockets.options = options
		server.settings.Timeout.Shutdown = 3 * time.Second
	})
	clients := make(map[string]*websocket.Conn, 4)
	for _, path := range []string{"/read", "/write", "/reject", "/quiet"} {
		client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, path), nil)
		require.NoError(t, err)
		requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
		clients[path] = client
		cleanupWebsocketClient(t, client)
	}
	messageType, message, err := clients["/write"].ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ready"), message)

	startedModes := make(map[string]bool, 4)
	for range 4 {
		select {
		case mode := <-started:
			startedModes[mode] = true
		case <-time.After(time.Second):
			t.Fatal("not all websocket application callbacks started")
		}
	}
	require.Equal(t, map[string]bool{"read": true, "write": true, "reject": true, "quiet": true}, startedModes)

	closer := &websocketOrderingCloser{t: t, active: &active, closed: make(chan struct{})}
	server.server.handlerClosers = []io.Closer{closer}
	server.cancel()

	for _, path := range []string{"/read", "/write", "/reject"} {
		closeErr := readWebsocketClientClose(t, clients[path])
		require.Equal(t, 1001, closeErr.Code)
		require.Equal(t, "server shutting down", closeErr.Text)
	}
	require.NoError(t, server.stopWithin(t, 1500*time.Millisecond))
	require.Zero(t, active.Load())
	require.EqualValues(t, 1, closer.closeCalls.Load())
	require.Zero(t, logger.errorCalls.Load())
}

func TestHttpServerShutdownReportsNoncooperatingWebsocketAndStillClosesHandlers(t *testing.T) {
	releaseCallback := make(chan struct{})
	callbackStarted := make(chan struct{})
	callbackDone := make(chan struct{})
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		close(callbackStarted)
		<-releaseCallback
		close(callbackDone)

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		options := defaultWebsocketOptions()
		options.CloseTimeout = 5 * time.Second
		server.websockets.options = options
		server.settings.Timeout.Shutdown = 150 * time.Millisecond
	})
	defer func() {
		select {
		case <-releaseCallback:
		default:
			close(releaseCallback)
		}
	}()

	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("websocket callback did not start")
	}

	closer := &shutdownTestHandler{closed: make(chan struct{})}
	server.server.handlerClosers = []io.Closer{closer}
	server.expectRunError = true
	server.cancel()
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1001, closeErr.Code)
	require.Equal(t, "server shutting down", closeErr.Text)

	shutdownErr := server.stop(t)
	require.Error(t, shutdownErr)
	require.ErrorIs(t, shutdownErr, context.DeadlineExceeded)
	require.Contains(t, strings.ToLower(shutdownErr.Error()), "websocket")
	require.EqualValues(t, 1, closer.closeCalls.Load())
	select {
	case <-closer.closed:
	default:
		t.Fatal("application handler closer was skipped on shutdown timeout")
	}

	close(releaseCallback)
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("noncooperating callback did not finish after release")
	}
}

func TestHttpServerShutdownFencesHijackedPendingHandshake(t *testing.T) {
	hijacked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHijack := func() {
		releaseOnce.Do(func() { close(release) })
	}

	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		c.Writer = &websocketBlockingHijackWriter{
			ResponseWriter: c.Writer,
			hijacked:       hijacked,
			release:        release,
		}
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router)

	t.Cleanup(releaseHijack)

	type dialResult struct {
		response *http.Response
		err      error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		dialDone <- dialResult{response: response, err: err}
	}()
	select {
	case <-hijacked:
	case <-time.After(time.Second):
		t.Fatal("websocket handshake did not reach the hijacked pending state")
	}

	server.server.websockets.stopAdmission()
	server.cancel()
	releaseHijack()

	var result dialResult
	select {
	case result = <-dialDone:
	case <-time.After(time.Second):
		t.Fatal("late raw socket attachment did not unblock the handshake")
	}
	require.Error(t, result.err)
	if result.response != nil {
		require.NotEqual(t, http.StatusSwitchingProtocols, result.response.StatusCode)
		require.Empty(t, result.response.Header.Get("Sec-WebSocket-Accept"))
		require.NoError(t, result.response.Body.Close())
	}
	require.False(t, called.Load())
	require.NoError(t, server.stop(t))
}

type websocketOrderingCloser struct {
	t          *testing.T
	active     *atomic.Int32
	closed     chan struct{}
	closeCalls atomic.Int32
	closeOnce  sync.Once
}

func (c *websocketOrderingCloser) Close() error {
	c.closeCalls.Add(1)
	if active := c.active.Load(); active != 0 {
		c.t.Errorf("application closer ran with %d websocket callbacks still active", active)
	}
	c.closeOnce.Do(func() { close(c.closed) })

	return nil
}

type websocketBlockingHijackWriter struct {
	gin.ResponseWriter
	hijacked chan struct{}
	release  <-chan struct{}
}

func (w *websocketBlockingHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if conn, rw, err := w.ResponseWriter.Hijack(); err != nil {
		return nil, nil, err
	} else {
		close(w.hijacked)
		<-w.release

		return conn, rw, nil
	}
}

func TestHttpServerShutdownInterruptsAlreadySelectedApplicationClose(t *testing.T) {
	closeControlWrites := make(chan int, 4)
	releaseWrite := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseWrite) })
	}
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		closeDone := make(chan error, 1)
		go func() {
			closeDone <- conn.CloseWithStatus(3001, "application selected")
		}()
		<-ctx.Done()
		<-closeDone

		return nil
	}))

	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))
	tracer := tracingMocks.NewInstrumentor(t)
	tracer.EXPECT().HttpHandler(router).Return(router).Once()
	settings := &Settings{Port: "0"}
	settings.Timeout.Shutdown = 200 * time.Millisecond
	server, err := NewWithInterfaces(t.Context(), logger, router, tracer, settings, shutdownTestMetricRecorder{})
	require.NoError(t, err)
	server.websockets.options.CloseTimeout = 5 * time.Second
	server.listener = &websocketFirstCloseListener{
		Listener:           server.listener,
		closeControlWrites: closeControlWrites,
		release:            releaseWrite,
	}
	port, err := server.GetPort()
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(t.Context())
	fixture := &websocketProtocolTestServer{
		server:  server,
		port:    *port,
		cancel:  cancel,
		runDone: make(chan error, 1),
	}
	go func() {
		fixture.runDone <- server.Run(runCtx)
	}()
	runJoined := false
	t.Cleanup(func() {
		release()
		cancel()
		if !runJoined {
			select {
			case <-fixture.runDone:
			case <-time.After(2 * time.Second):
				t.Error("websocket server Run goroutine did not finish after releasing the close write")
			}
		}
	})

	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(fixture, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	select {
	case code := <-closeControlWrites:
		require.Equal(t, 3001, code)
	case <-time.After(time.Second):
		t.Fatal("application-selected close control was not written")
	}
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 3001, closeErr.Code)
	require.Equal(t, "application selected", closeErr.Text)

	closer := &shutdownTestHandler{closed: make(chan struct{})}
	server.handlerClosers = []io.Closer{closer}
	shutdownStarted := time.Now()
	cancel()
	select {
	case runErr := <-fixture.runDone:
		runJoined = true
		require.NoError(t, runErr)
	case <-time.After(time.Second):
		t.Fatal("shutdown did not hard-close the selected close write within its budget")
	}
	require.Less(t, time.Since(shutdownStarted), time.Second)
	for len(closeControlWrites) > 0 {
		require.Equal(t, 3001, <-closeControlWrites, "shutdown must preserve the first selected close status")
	}
	require.EqualValues(t, 1, closer.closeCalls.Load())
	select {
	case <-closer.closed:
	default:
		t.Fatal("application handler closer was skipped")
	}
}

func TestHttpServerShutdownDoesNotLogExpectedCloseDeadlineFailure(t *testing.T) {
	deadlines := make(chan time.Time, 1)
	started := make(chan struct{})
	logger := newWebsocketCountingLogger(t)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, _ *WebsocketConn) error {
		close(started)
		<-ctx.Done()

		return nil
	}))
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger, func(server *HttpServer) {
		server.settings.Timeout.Shutdown = time.Second
		server.websockets.options.CloseTimeout = 50 * time.Millisecond
		server.listener = &websocketBlockedCloseListener{Listener: server.listener, deadlines: deadlines}
	})

	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("websocket callback did not start")
	}

	require.NoError(t, server.stopWithin(t, time.Second))
	select {
	case deadline := <-deadlines:
		require.False(t, deadline.IsZero())
	case <-time.After(time.Second):
		t.Fatal("shutdown did not attempt its close control")
	}
	require.Zero(t, logger.errorCalls.Load(), "expected close deadline failures must not be logged as application failures")
}

type websocketFirstCloseListener struct {
	net.Listener
	closeControlWrites chan<- int
	release            <-chan struct{}
}

func (l *websocketFirstCloseListener) Accept() (net.Conn, error) {
	if conn, err := l.Listener.Accept(); err != nil {
		return nil, fmt.Errorf("accept websocket close-race connection: %w", err)
	} else {
		return &websocketFirstCloseConn{
			Conn:               conn,
			closeControlWrites: l.closeControlWrites,
			release:            l.release,
			closed:             make(chan struct{}),
		}, nil
	}
}

type websocketFirstCloseConn struct {
	net.Conn
	closeControlWrites chan<- int
	release            <-chan struct{}
	closed             chan struct{}
	handshakeComplete  atomic.Bool
	closeOnce          sync.Once
}

func (c *websocketFirstCloseConn) Write(data []byte) (int, error) {
	if !c.handshakeComplete.Load() {
		if n, err := c.Conn.Write(data); err != nil {
			return n, fmt.Errorf("write websocket close-race handshake: %w", err)
		} else {
			c.handshakeComplete.Store(true)

			return n, nil
		}
	}
	if len(data) == 0 || data[0]&0x0f != websocket.CloseMessage {
		return c.Conn.Write(data)
	}

	code := 0
	if len(data) >= 4 {
		code = int(data[2])<<8 | int(data[3])
	}
	var n int
	var err error
	if n, err = c.Conn.Write(data); err != nil {
		return n, fmt.Errorf("write websocket close-race control: %w", err)
	}
	if n != len(data) {
		return n, io.ErrShortWrite
	}
	c.closeControlWrites <- code
	select {
	case <-c.release:
	case <-c.closed:
	}

	return n, nil
}

func (c *websocketFirstCloseConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	if err := c.Conn.Close(); err != nil {
		return fmt.Errorf("close websocket close-race connection: %w", err)
	}

	return nil
}
