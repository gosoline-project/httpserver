package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/justtrackio/gosoline/pkg/appctx"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/stretchr/testify/require"
)

func TestWebsocketProtocolEdgesConcurrentCloseSelection(t *testing.T) {
	closures := []struct {
		code   int
		reason string
	}{
		{code: websocket.ClosePolicyViolation, reason: "policy selected"},
		{code: websocket.CloseTryAgainLater, reason: "retry selected"},
		{code: 4001, reason: "application one"},
		{code: 4002, reason: "application two"},
	}
	ready := make(chan struct{}, len(closures))
	start := make(chan struct{})
	attemptErrors := make(chan error, len(closures))
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		var workers sync.WaitGroup
		for _, closure := range closures {
			closure := closure
			workers.Add(1)
			go func() {
				defer workers.Done()
				ready <- struct{}{}
				<-start
				attemptErrors <- conn.CloseWithStatus(closure.code, closure.reason)
			}()
		}
		workers.Wait()
		close(attemptErrors)
		for err := range attemptErrors {
			if err != nil {
				return fmt.Errorf("select concurrent websocket close status: %w", err)
			}
		}

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	for range closures {
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("concurrent close callers did not reach the start barrier")
		}
	}
	close(start)

	closeErr := readWebsocketClientClose(t, client)
	selected := false
	for _, closure := range closures {
		if closeErr.Code == closure.code && closeErr.Text == closure.reason {
			selected = true

			break
		}
	}
	require.True(t, selected, "received close status %d %q was not selected by a caller", closeErr.Code, closeErr.Text)
}

func TestWebsocketProtocolEdgesRejectsUnfinishedFragmentHeader(t *testing.T) {
	const reason = "server stream does not accept messages"
	readerStarted := make(chan struct{})
	callbackCanceled := make(chan error, 1)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		var err error
		if err = conn.RejectIncomingMessages(reason); err != nil {
			return fmt.Errorf("enable websocket receive rejection: %w", err)
		}
		close(readerStarted)
		<-ctx.Done()
		callbackCanceled <- ctx.Err()

		return fmt.Errorf("websocket callback context canceled: %w", ctx.Err())
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	select {
	case <-readerStarted:
	case <-time.After(time.Second):
		t.Fatal("reject-mode reader did not start")
	}

	// Send the complete masked header for a valid fragmented text message, but
	// deliberately leave its body and continuation frames unfinished.
	require.NoError(t, writeMaskedWebsocketFrameHeader(client.UnderlyingConn(), false, websocket.TextMessage, 12))
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, websocket.ClosePolicyViolation, closeErr.Code)
	require.Equal(t, reason, closeErr.Text)
	select {
	case err := <-callbackCanceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("rejecting the data-message header did not cancel the callback")
	}
}

func TestWebsocketProtocolEdgesNegotiatesHTTPRejections(t *testing.T) {
	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocket[websocketQueryInput](func(context.Context, *websocketQueryInput, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router)

	_, malformed, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket?count=bad"), nil)
	require.Error(t, err)
	requireWebsocketStatus(t, malformed, http.StatusBadRequest)
	assertNegotiatedWebsocketErrorBody(t, malformed)

	missingUpgrade := sendWebsocketHandshakeRequest(t, server, "13", false)
	require.Equal(t, http.StatusUpgradeRequired, missingUpgrade.StatusCode)
	require.Equal(t, "websocket", missingUpgrade.Header.Get("Upgrade"), "426 must advertise the protocol the client should upgrade to")
	require.Equal(t, "13", missingUpgrade.Header.Get("Sec-WebSocket-Version"))
	require.NoError(t, missingUpgrade.Body.Close())

	server.server.websockets.stopAdmission()
	shuttingDown := sendWebsocketHandshakeRequest(t, server, "13", true)
	require.Equal(t, http.StatusServiceUnavailable, shuttingDown.StatusCode)
	assertNegotiatedWebsocketErrorBody(t, shuttingDown)
	require.False(t, called.Load())
}

func assertNegotiatedWebsocketErrorBody(t *testing.T, response *http.Response) {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	require.NoError(t, closeErr, "close negotiated websocket error body")
	require.NoError(t, err, "read negotiated websocket error body")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload), "decode negotiated websocket error body")
	require.NotEmpty(t, payload["err"], "negotiated error response must contain the standard error field")
}

func TestWebsocketProtocolEdgesFiltersMixedCaseHandshakeHeaders(t *testing.T) {
	addHeaders := func(c *gin.Context) {
		header := c.Writer.Header()
		header.Add("Set-Cookie", "first=1; Path=/")
		header.Add("Set-Cookie", "second=2; Path=/")
		header["Connection"] = []string{"close, X-Remove-One", "keep-alive, x-Remove-Two"}
		header["cOnNeCtIoN"] = []string{"proxy-connection, X-Remove-Three"}
		header["x-rEmOvE-OnE"] = []string{"one"}
		header["X-REMOVE-TWO"] = []string{"two"}
		header["X-Remove-Three"] = []string{"three"}
		header["sEc-WeBsOcKeT-PrOtOcOl"] = []string{"middleware-protocol"}
		header["sEc-WeBsOcKeT-Extensions"] = []string{"middleware-extension"}
		header["uPgRaDe"] = []string{"h2c"}
		header["cOnTeNt-TyPe"] = []string{"text/plain"}
		header["pRoXy-AuThEnTiCaTe"] = []string{"Basic realm=ignored"}
		header["X-Repeated"] = []string{"first", "second"}
		c.Next()
	}

	router := newWebsocketProtocolTestRouter()
	router.Use(addHeaders)
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		var err error
		if err = conn.Send([]byte("ready")); err != nil {
			return fmt.Errorf("send handshake test frame: %w", err)
		}

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)

	require.Equal(t, []string{"first=1; Path=/", "second=2; Path=/"}, response.Header.Values("Set-Cookie"))
	require.Equal(t, []string{"first", "second"}, response.Header.Values("X-Repeated"))
	require.Equal(t, []string{"Upgrade"}, response.Header.Values("Connection"))
	require.Equal(t, "websocket", response.Header.Get("Upgrade"))
	for _, excluded := range []string{
		"X-Remove-One", "X-Remove-Two", "X-Remove-Three", "Sec-WebSocket-Protocol",
		"Sec-WebSocket-Extensions", "Content-Type", "Proxy-Authenticate",
	} {
		require.Empty(t, response.Header.Values(excluded), "filtered handshake included %s", excluded)
	}
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ready"), message)
}

func TestWebsocketProtocolEdgesAbruptEOFCancelsWithOriginalCause(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	callbackResult := make(chan struct {
		contextErr error
		readErr    error
	}, 1)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		_, _, readErr := conn.ReadMessage()
		callbackResult <- struct {
			contextErr error
			readErr    error
		}{contextErr: ctx.Err(), readErr: readErr}

		return fmt.Errorf("read message after abrupt peer EOF: %w", readErr)
	}))
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)

	// Closing the TCP socket directly omits a WebSocket close frame.
	require.NoError(t, client.UnderlyingConn().Close())
	select {
	case result := <-callbackResult:
		require.ErrorIs(t, result.contextErr, context.Canceled)
		require.ErrorIs(t, result.readErr, ErrClientDisconnected)
		var closeErr *websocket.CloseError
		require.ErrorAs(t, result.readErr, &closeErr)
		require.Equal(t, websocket.CloseAbnormalClosure, closeErr.Code)
		require.Equal(t, "unexpected EOF", closeErr.Text)
		require.True(t, websocketExpectedError(result.readErr))
	case <-time.After(2 * time.Second):
		t.Fatal("abrupt peer EOF did not cancel the callback and unblock its read")
	}
	require.NoError(t, server.stop(t))
	require.Zero(t, logger.errorCalls.Load())
}

func TestWebsocketProtocolEdgesTerminalWriteFailureCancelsWithCause(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	writeAllowed := make(chan struct{})
	writeStarted := make(chan struct{})
	callbackResult := make(chan struct {
		contextErr error
		writeErr   error
	}, 1)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		close(writeStarted)
		<-writeAllowed
		payload := make([]byte, 64*1024)
		var writeErr error
		for range 128 {
			writeErr = conn.Send(payload)
			if writeErr != nil {
				break
			}
		}
		callbackResult <- struct {
			contextErr error
			writeErr   error
		}{contextErr: ctx.Err(), writeErr: writeErr}
		if writeErr == nil {
			return errors.New("peer reset was not observed within the bounded write attempts")
		}

		return fmt.Errorf("send after abrupt peer reset: %w", writeErr)
	}))
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger, func(server *HttpServer) {
		options := defaultWebsocketOptions()
		options.WriteTimeout = time.Second
		server.websockets.options = options
	})
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("application callback did not reach terminal write barrier")
	}
	tcp, ok := client.UnderlyingConn().(*net.TCPConn)
	require.True(t, ok, "loopback websocket transport must be TCP")
	require.NoError(t, tcp.SetLinger(0))
	require.NoError(t, tcp.Close())
	close(writeAllowed)

	select {
	case result := <-callbackResult:
		require.Error(t, result.writeErr)
		require.ErrorIs(t, result.writeErr, ErrClientDisconnected)
		require.True(t, websocketExpectedError(result.writeErr))
		require.ErrorIs(t, result.contextErr, context.Canceled)
		var operationErr *net.OpError
		require.ErrorAs(t, result.writeErr, &operationErr)
		require.True(t,
			errors.Is(result.writeErr, syscall.ECONNRESET) || errors.Is(result.writeErr, syscall.EPIPE),
			"terminal write should preserve the reset/broken-pipe cause: %v", result.writeErr,
		)
	case <-time.After(3 * time.Second):
		t.Fatal("terminal write did not observe the peer reset and cancel the callback")
	}
	require.NoError(t, server.stop(t))
	require.Zero(t, logger.errorCalls.Load())
}

func TestWebsocketProtocolEdgesNewServerWithSettingsMiddlewareAndAccounting(t *testing.T) {
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	defer release()
	var protectedCallbackCalled atomic.Bool
	routerFactory := websocketProtocolEdgesRouterFactory(handlerStarted, releaseHandler, &protectedCallbackCalled)
	handlerDone := make(chan struct{})
	var errorLog websocketProtocolEdgesHTTPErrorLog
	// Check net/http diagnostics only after outer ServeHTTP completes, ensuring
	// deferred middleware writes have had a chance to report.
	server := startWebsocketProtocolEdgesConfiguredServer(t, routerFactory, func(server *http.Server) {
		server.ErrorLog = stdlog.New(&errorLog, "", 0)
		originalHandler := server.Handler
		server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/socket" {
				defer close(handlerDone)
			}
			originalHandler.ServeHTTP(writer, request)
		})
	})
	_, denied, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/protected"), nil)
	require.Error(t, err)
	requireWebsocketStatus(t, denied, http.StatusUnauthorized)
	require.Empty(t, denied.Header.Get("Sec-WebSocket-Accept"))
	require.NoError(t, denied.Body.Close())
	require.False(t, protectedCallbackCalled.Load(), "protected route denial must happen before upgrading")

	dialer := newWebsocketLowercaseConnectionHeaderDialer()
	headers := http.Header{"Accept-Encoding": {"gzip"}}
	client, response, err := dialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), headers)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	require.Equal(t, []string{"Upgrade"}, response.Header.Values("Connection"))
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("configured server did not invoke the WebSocket callback")
	}
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, "production stack ready", string(message))

	requestMetrics, ok := server.server.metricRecorder.(*serverMetricRecorder)
	require.True(t, ok, "NewServerWithSettings should install the production request metric recorder")
	require.EqualValues(t, 1, requestMetrics.activeRequests.Load(), "a live WebSocket callback remains an active HTTP request")

	payload := []byte{0, 1, 2, 255}
	require.NoError(t, client.WriteMessage(websocket.BinaryMessage, payload))
	messageType, message, err = client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	require.Equal(t, payload, message, "production gzip middleware must not corrupt the duplex WebSocket stream")
	release()
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, websocket.CloseNormalClosure, closeErr.Code)
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("configured server did not finish its full middleware chain")
	}
	require.NotContains(t, errorLog.String(), "response.Write on hijacked connection")
}

func websocketProtocolEdgesRouterFactory(handlerStarted chan<- struct{}, releaseHandler <-chan struct{}, protectedCallbackCalled *atomic.Bool) RouterFactory {
	return func(_ context.Context, _ cfg.Config, _ log.Logger, router *Router) error {
		router.GET("/protected",
			func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) },
			BindWebsocketN(func(context.Context, *WebsocketConn) error {
				protectedCallbackCalled.Store(true)

				return nil
			}),
		)
		router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
			var err error
			if err = conn.Send([]byte("production stack ready")); err != nil {
				return fmt.Errorf("send production-stack websocket greeting: %w", err)
			}
			close(handlerStarted)
			var messageType WebsocketMessageType
			var payload []byte
			if messageType, payload, err = conn.ReadMessage(); err != nil {
				return fmt.Errorf("read production-stack websocket message: %w", err)
			}
			if messageType != WebsocketBinaryMessage {
				return fmt.Errorf("expected a binary websocket message, got type %d", messageType)
			}
			if err = conn.SendBinary(payload); err != nil {
				return fmt.Errorf("echo production-stack websocket message: %w", err)
			}
			select {
			case <-releaseHandler:
				return nil
			case <-ctx.Done():
				return fmt.Errorf("wait for configured websocket release: %w", ctx.Err())
			}
		}))

		return nil
	}
}

func startWebsocketProtocolEdgesConfiguredServer(t *testing.T, routerFactory RouterFactory, configureServer func(*http.Server)) *websocketProtocolTestServer {
	t.Helper()
	settings := &Settings{
		Port: "0",
		Mode: gin.TestMode,
		Compression: CompressionSettings{
			Level:         "default",
			Decompression: true,
		},
		Timeout: TimeoutSettings{Shutdown: 2 * time.Second},
	}
	factory := NewServerWithSettings(
		t.Context(),
		"test",
		routerFactory,
		settings,
		WithWebsocketOptions(WebsocketOptions{}),
	)
	ctx := appctx.WithContainer(t.Context())
	config := cfg.New(map[string]any{
		"httpserver": map[string]any{
			"test": map[string]any{
				"connection_lifecycle": map[string]any{
					"enabled":           true,
					"max_request_count": 1,
				},
			},
		},
		"tracing": map[string]any{"provider": "noop"},
	})
	logger := log.NewLogger()
	var (
		server *HttpServer
		err    error
	)
	if _, err = kernel.BuildKernel(ctx, config, logger, []kernel.Option{
		kernel.WithModuleFactory("httpserver-test", func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
			var (
				module kernel.Module
				err    error
			)
			if module, err = factory(ctx, config, logger); err != nil {
				return nil, fmt.Errorf("create NewServerWithSettings test module: %w", err)
			}
			server = module.(*HttpServer)

			return server, nil
		}),
	}); err != nil {
		t.Fatalf("build configured websocket test kernel: %v", err)
	}
	require.NotNil(t, server)
	port, err := server.GetPort()
	require.NoError(t, err)
	if configureServer != nil {
		configureServer(server.server)
	}
	runCtx, cancel := context.WithCancel(t.Context())
	fixture := &websocketProtocolTestServer{
		server:  server,
		port:    *port,
		cancel:  cancel,
		runDone: make(chan error, 1),
	}
	go func() { fixture.runDone <- server.Run(runCtx) }()
	t.Cleanup(func() {
		if err := fixture.stop(t); err != nil {
			t.Errorf("stop configured websocket test server: %v", err)
		}
	})

	return fixture
}

func newWebsocketLowercaseConnectionHeaderDialer() *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var (
			conn net.Conn
			err  error
		)
		if conn, err = (&net.Dialer{}).DialContext(ctx, network, address); err != nil {
			return nil, fmt.Errorf("dial configured websocket test server: %w", err)
		}

		return &websocketLowercaseConnectionHeaderConn{Conn: conn}, nil
	}

	return &dialer
}

type websocketLowercaseConnectionHeaderConn struct {
	net.Conn
	mu        sync.Mutex
	pending   []byte
	handshook bool
}

func (c *websocketLowercaseConnectionHeaderConn) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handshook {
		return c.Conn.Write(data)
	}

	c.pending = append(c.pending, data...)
	requestEnd := bytes.Index(c.pending, []byte("\r\n\r\n"))
	if requestEnd < 0 {
		return len(data), nil
	}
	requestEnd += len("\r\n\r\n")
	request := append([]byte(nil), c.pending[:requestEnd]...)
	remaining := append([]byte(nil), c.pending[requestEnd:]...)
	const canonicalConnection = "\r\nConnection: Upgrade\r\n"
	if !bytes.Contains(request, []byte(canonicalConnection)) {
		return 0, errors.New("websocket handshake omitted the expected Connection header")
	}
	request = bytes.Replace(request, []byte(canonicalConnection), []byte("\r\nConnection: upgrade\r\n"), 1)
	var err error
	if err = writeWebsocketBytes(c.Conn, request); err != nil {
		return 0, fmt.Errorf("write rewritten websocket handshake: %w", err)
	}
	if len(remaining) != 0 {
		if err = writeWebsocketBytes(c.Conn, remaining); err != nil {
			return 0, fmt.Errorf("write buffered websocket data: %w", err)
		}
	}
	c.pending = nil
	c.handshook = true

	return len(data), nil
}

type websocketProtocolEdgesHTTPErrorLog struct {
	mu       sync.Mutex
	contents bytes.Buffer
}

func (l *websocketProtocolEdgesHTTPErrorLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.contents.Write(data)
}

func (l *websocketProtocolEdgesHTTPErrorLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.contents.String()
}
