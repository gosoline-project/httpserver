package httpserver

import (
	"bufio"
	"bytes"
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
	"github.com/stretchr/testify/require"
)

func TestWebsocketRequestCancellationAfterFuncIsJoinedBeforeMiddlewareReturns(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	firstCloseDone := make(chan struct{})
	handlerReturned := make(chan struct{})
	middlewareReturned := make(chan struct{})
	cancelRequest := make(chan context.CancelFunc, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseClose) })
	}

	logger := newWebsocketCountingLogger(t)
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		requestCtx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(requestCtx)
		cancelRequest <- cancel
		c.Next()
		close(middlewareReturned)
	})
	router.Use(func(c *gin.Context) {
		c.Writer = &websocketLifecycleHijackResponseWriter{
			ResponseWriter: c.Writer,
			wrapConn: func(conn net.Conn) net.Conn {
				return &websocketLifecycleCancellationGateConn{
					Conn:           conn,
					closeStarted:   closeStarted,
					releaseClose:   releaseClose,
					firstCloseDone: firstCloseDone,
				}
			},
		}
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(_ context.Context, _ *WebsocketConn) error {
		defer close(handlerReturned)
		cancel := <-cancelRequest
		cancel()
		select {
		case <-closeStarted:
		case <-time.After(2 * time.Second):
			return errors.New("request cancellation did not start its transport-close callback")
		}

		return nil
	}))

	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	t.Cleanup(release)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)

	websocketLifecycleWait(t, handlerReturned, "application callback did not return")
	select {
	case <-middlewareReturned:
		t.Fatal("Gin middleware returned before the already-running request cancellation callback completed")
	case <-time.After(250 * time.Millisecond):
	}

	release()
	websocketLifecycleWait(t, firstCloseDone, "request cancellation callback did not finish closing the transport")
	websocketLifecycleWait(t, middlewareReturned, "binder did not finish after the request cancellation callback completed")
	require.NoError(t, server.stop(t))
	require.Zero(t, logger.errorCalls.Load())
}

func TestHttpServerShutdownClosesAttachedRawSocketBeforePending101Write(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	rawClosed := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseWrite) })
	}

	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		c.Writer = &websocketLifecycleHijackResponseWriter{
			ResponseWriter: c.Writer,
			wrapConn: func(conn net.Conn) net.Conn {
				return &websocketLifecycleObservedConn{Conn: conn, closed: rawClosed}
			},
			handshakeGate: &websocketLifecycleHandshakeGate{
				writeStarted:       writeStarted,
				releaseBeforeWrite: releaseWrite,
			},
		}
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		server.settings.Timeout.Drain = 0
	})
	t.Cleanup(release)

	type dialResult struct {
		response *http.Response
		err      error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		dialDone <- dialResult{response: response, err: err}
	}()
	websocketLifecycleWait(t, writeStarted, "upgrade did not reach the gated 101 write")

	server.cancel()
	websocketLifecycleWait(t, rawClosed, "shutdown did not close the raw socket attached before the 101 write")
	release()

	var result dialResult
	select {
	case result = <-dialDone:
	case <-time.After(2 * time.Second):
		t.Fatal("gated handshake did not finish after shutdown closed and released it")
	}
	require.Error(t, result.err)
	if result.response != nil {
		require.NotEqual(t, http.StatusSwitchingProtocols, result.response.StatusCode)
		require.NoError(t, result.response.Body.Close())
	}
	require.False(t, called.Load())
	require.NoError(t, server.stop(t))
}

func TestHttpServerShutdownFencesCompletedWire101BeforePromotion(t *testing.T) {
	writeStarted := make(chan struct{})
	wire101Written := make(chan struct{})
	releaseAfterWire := make(chan struct{})
	rawClosed := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseAfterWire) })
	}

	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		c.Writer = &websocketLifecycleHijackResponseWriter{
			ResponseWriter: c.Writer,
			wrapConn: func(conn net.Conn) net.Conn {
				return &websocketLifecycleObservedConn{Conn: conn, closed: rawClosed}
			},
			handshakeGate: &websocketLifecycleHandshakeGate{
				writeStarted:     writeStarted,
				wire101Written:   wire101Written,
				releaseAfterWire: releaseAfterWire,
			},
		}
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		server.settings.Timeout.Drain = 0
	})
	t.Cleanup(release)

	type dialResult struct {
		client   *websocket.Conn
		response *http.Response
		err      error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		dialDone <- dialResult{client: client, response: response, err: err}
	}()
	websocketLifecycleWait(t, writeStarted, "upgrade did not reach the gated 101 write")
	websocketLifecycleWait(t, wire101Written, "the complete 101 response was not flushed to the client")

	var result dialResult
	select {
	case result = <-dialDone:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not observe the completed 101 response")
	}
	require.NoError(t, result.err)
	requireWebsocketStatus(t, result.response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, result.client)

	server.cancel()
	websocketLifecycleWait(t, rawClosed, "shutdown did not close the attached raw socket before promotion")
	release()
	require.NoError(t, server.stop(t))
	require.False(t, called.Load())

	require.NoError(t, result.client.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, readErr := result.client.ReadMessage()
	require.Error(t, readErr)
	var closeErr *websocket.CloseError
	if errors.As(readErr, &closeErr) {
		require.Equal(t, websocket.CloseAbnormalClosure, closeErr.Code, "an unpromoted raw socket must be hard-closed without a WebSocket close frame")
	}
}

func TestHttpServerShutdownTimesOutWaitingForOriginValidationAndStillRunsClosers(t *testing.T) {
	originStarted := make(chan struct{})
	releaseOrigin := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseOrigin) })
	}
	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		options := defaultWebsocketOptions()
		options.CheckOrigin = func(*http.Request) bool {
			close(originStarted)
			<-releaseOrigin

			return true
		}
		server.websockets.options = options
		server.settings.Timeout.Drain = 0
		server.settings.Timeout.Shutdown = 150 * time.Millisecond
	})
	server.expectRunError = true
	t.Cleanup(release)

	type dialResult struct {
		response *http.Response
		err      error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		headers := http.Header{"Origin": {"https://trusted.example"}}
		_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), headers)
		dialDone <- dialResult{response: response, err: err}
	}()
	websocketLifecycleWait(t, originStarted, "request did not reach pre-hijack origin validation")

	closer := &shutdownTestHandler{closed: make(chan struct{})}
	server.server.handlerClosers = []io.Closer{closer}
	server.cancel()
	shutdownErr := server.stop(t)
	require.Error(t, shutdownErr)
	require.ErrorIs(t, shutdownErr, context.DeadlineExceeded)
	require.Contains(t, strings.ToLower(shutdownErr.Error()), "websocket shutdown")
	require.Contains(t, shutdownErr.Error(), "wait for websocket requests and callbacks")
	require.EqualValues(t, 1, closer.closeCalls.Load())
	select {
	case <-closer.closed:
	default:
		t.Fatal("application handler closer was skipped after shutdown timeout")
	}

	release()
	var result dialResult
	select {
	case result = <-dialDone:
	case <-time.After(2 * time.Second):
		t.Fatal("late origin validation did not finish after release")
	}
	require.Error(t, result.err)
	if result.response != nil {
		require.NotEqual(t, http.StatusSwitchingProtocols, result.response.StatusCode)
		require.NoError(t, result.response.Body.Close())
	}
	require.False(t, called.Load())
}

func TestBindWebsocketFailedHijackAfterGinCommitDoesNotRenderAnotherResponse(t *testing.T) {
	var called atomic.Bool
	var responseWriter atomic.Pointer[websocketLifecycleCommittedHijackFailureWriter]
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		writer := &websocketLifecycleCommittedHijackFailureWriter{ResponseWriter: c.Writer}
		responseWriter.Store(writer)
		c.Writer = writer
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router)
	response := sendWebsocketHandshakeRequest(t, server, "13", true)
	require.Equal(t, http.StatusTeapot, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Empty(t, body)
	require.NoError(t, response.Body.Close())
	require.False(t, called.Load())
	writer := responseWriter.Load()
	require.NotNil(t, writer)
	require.Zero(t, writer.postCommitOperations.Load(), "middleware must not attempt a second HTTP response after Hijack failed on a committed writer")
	require.NoError(t, server.stop(t))
}

func websocketLifecycleWait(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

type websocketLifecycleHijackResponseWriter struct {
	gin.ResponseWriter
	wrapConn      func(net.Conn) net.Conn
	handshakeGate *websocketLifecycleHandshakeGate
}

func (w *websocketLifecycleHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	var conn net.Conn
	var readWriter *bufio.ReadWriter
	var err error
	if conn, readWriter, err = w.ResponseWriter.Hijack(); err != nil {
		return nil, nil, fmt.Errorf("hijack lifecycle test connection: %w", err)
	}

	if w.wrapConn != nil {
		conn = w.wrapConn(conn)
	}
	if w.handshakeGate != nil {
		w.handshakeGate.Conn = conn
		conn = w.handshakeGate
	}

	return conn, readWriter, nil
}

// Gorilla writes its 101 response to the hijacked net.Conn, not its buffered
// reader/writer, so this gate controls the raw write and leaves buffering intact.
type websocketLifecycleHandshakeGate struct {
	net.Conn
	writeStarted       chan struct{}
	releaseBeforeWrite <-chan struct{}
	wire101Written     chan struct{}
	releaseAfterWire   <-chan struct{}
	startedOnce        sync.Once
	writtenOnce        sync.Once
}

func (w *websocketLifecycleHandshakeGate) Write(data []byte) (int, error) {
	if !bytes.Contains(data, []byte("HTTP/1.1 101")) {
		return w.Conn.Write(data)
	}
	if w.writeStarted != nil {
		w.startedOnce.Do(func() { close(w.writeStarted) })
	}
	if w.releaseBeforeWrite != nil {
		<-w.releaseBeforeWrite
	}

	var written int
	var err error
	if written, err = w.Conn.Write(data); err != nil {
		return written, fmt.Errorf("write lifecycle test handshake: %w", err)
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if w.wire101Written != nil {
		w.writtenOnce.Do(func() { close(w.wire101Written) })
	}
	if w.releaseAfterWire != nil {
		<-w.releaseAfterWire
	}

	return written, nil
}

type websocketLifecycleObservedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *websocketLifecycleObservedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })

	return err
}

type websocketLifecycleCancellationGateConn struct {
	net.Conn
	closeStarted   chan struct{}
	releaseClose   <-chan struct{}
	firstCloseDone chan struct{}
	firstClose     atomic.Bool
}

func (c *websocketLifecycleCancellationGateConn) Close() error {
	if c.firstClose.CompareAndSwap(false, true) {
		close(c.closeStarted)
		<-c.releaseClose
		err := c.Conn.Close()
		close(c.firstCloseDone)

		return err
	}

	return nil
}

type websocketLifecycleCommittedHijackFailureWriter struct {
	gin.ResponseWriter
	postCommitOperations atomic.Int32
}

func (w *websocketLifecycleCommittedHijackFailureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.ResponseWriter.WriteHeader(http.StatusTeapot)
	w.ResponseWriter.WriteHeaderNow()

	return nil, nil, errors.New("forced websocket Hijack failure after Gin committed the response")
}

func (w *websocketLifecycleCommittedHijackFailureWriter) WriteHeader(statusCode int) {
	w.recordPostCommitOperation()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *websocketLifecycleCommittedHijackFailureWriter) WriteHeaderNow() {
	w.recordPostCommitOperation()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *websocketLifecycleCommittedHijackFailureWriter) Write(data []byte) (int, error) {
	w.recordPostCommitOperation()

	return w.ResponseWriter.Write(data)
}

func (w *websocketLifecycleCommittedHijackFailureWriter) WriteString(data string) (int, error) {
	w.recordPostCommitOperation()

	return w.ResponseWriter.WriteString(data)
}

func (w *websocketLifecycleCommittedHijackFailureWriter) Flush() {
	w.recordPostCommitOperation()
	w.ResponseWriter.Flush()
}

func (w *websocketLifecycleCommittedHijackFailureWriter) recordPostCommitOperation() {
	if w.Written() {
		w.postCommitOperations.Add(1)
	}
}
