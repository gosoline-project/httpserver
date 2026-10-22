package httpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/justtrackio/gosoline/pkg/log"
	logMocks "github.com/justtrackio/gosoline/pkg/log/mocks"
	"github.com/justtrackio/gosoline/pkg/metric"
	tracingMocks "github.com/justtrackio/gosoline/pkg/tracing/mocks"
	"github.com/stretchr/testify/require"
)

func cleanupWebsocketClient(t *testing.T, client *websocket.Conn) {
	t.Helper()
	t.Cleanup(func() {
		if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close websocket test client: %v", err)
		}
	})
}

func TestBindWebsocketEchoesTextAndBinaryMessages(t *testing.T) {
	router := newWebsocketProtocolTestRouter()

	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		var (
			messageType WebsocketMessageType
			data        []byte
			err         error
		)
		for {
			if messageType, data, err = conn.ReadMessage(); err != nil {
				return err
			}

			switch messageType {
			case WebsocketTextMessage:
				err = conn.Send(data)
			case WebsocketBinaryMessage:
				err = conn.SendBinary(data)
			default:
				return fmt.Errorf("unexpected websocket message type %d", messageType)
			}
			if err != nil {
				return fmt.Errorf("echo websocket message: %w", err)
			}
		}
	}))

	server := startWebsocketProtocolTestServer(t, router)
	url := fmt.Sprintf("ws://127.0.0.1:%d/socket", server.port)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	require.NoError(t, err)
	require.NotNil(t, response)
	cleanupWebsocketClient(t, client)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("hello")))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("hello"), message)

	require.NoError(t, client.WriteMessage(websocket.BinaryMessage, []byte{0, 1, 255}))
	messageType, message, err = client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	require.Equal(t, []byte{0, 1, 255}, message)
}

type websocketQueryInput struct {
	Count int `form:"count"`
}

func TestBindWebsocketRPreservesBoundInputRequestAndGinContext(t *testing.T) {
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		c.Set("test-value", "from-gin")
		c.Next()
	})
	router.GET("/socket", BindWebsocketR[websocketQueryInput](func(ctx context.Context, request *http.Request, input *websocketQueryInput, conn *WebsocketConn) error {
		value, ok := ctx.Value("test-value").(string)
		if !ok {
			return errors.New("gin context value was not preserved")
		}

		return conn.Send([]byte(fmt.Sprintf("%d|%s|%s|%s", input.Count, request.Method, request.Header.Get("X-Test"), value)))
	}))

	server := startWebsocketProtocolTestServer(t, router)
	headers := http.Header{"X-Test": {"present"}}
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket?count=7"), headers)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, "7|GET|present|from-gin", string(message))
}

func TestBindWebsocketInvalidInputReturnsHTTP400WithoutCallingHandler(t *testing.T) {
	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocket[websocketQueryInput](func(context.Context, *websocketQueryInput, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router)
	_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket?count=not-an-int"), nil)
	require.Error(t, err)
	requireWebsocketStatus(t, response, http.StatusBadRequest)
	require.NotEqual(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.False(t, called.Load())
	require.NoError(t, response.Body.Close())
}

func TestBindWebsocketOriginPolicy(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		noOrigin    bool
		accepted    bool
		customAllow bool
	}{
		{name: "same host", origin: "same-host", accepted: true},
		{name: "no origin", noOrigin: true, accepted: true},
		{name: "hostile origin", origin: "https://attacker.example"},
		{name: "custom origin override", origin: "https://attacker.example", accepted: true, customAllow: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var called atomic.Int32
			router := newWebsocketProtocolTestRouter()
			router.GET("/socket", BindWebsocketNR(func(_ context.Context, request *http.Request, conn *WebsocketConn) error {
				called.Add(1)

				return conn.Send([]byte(request.Header.Get("Origin")))
			}))

			var configure []func(*HttpServer)
			if test.customAllow {
				options := defaultWebsocketOptions()
				options.CheckOrigin = func(*http.Request) bool { return true }
				configure = append(configure, func(server *HttpServer) {
					server.websockets.options = options
				})
			}
			server := startWebsocketProtocolTestServer(t, router, configure...)
			headers := make(http.Header)
			switch {
			case test.noOrigin:
				headers["Origin"] = []string{}
			case test.origin == "same-host":
				headers.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d", server.port))
			case test.origin != "":
				headers.Set("Origin", test.origin)
			}

			client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), headers)
			if !test.accepted {
				require.Error(t, err)
				requireWebsocketStatus(t, response, http.StatusForbidden)
				require.NotEqual(t, http.StatusSwitchingProtocols, response.StatusCode)
				require.Zero(t, called.Load())
				require.NoError(t, response.Body.Close())

				return
			}

			require.NoError(t, err)
			requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
			cleanupWebsocketClient(t, client)
			require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
			messageType, message, readErr := client.ReadMessage()
			require.NoError(t, readErr)
			require.Equal(t, websocket.TextMessage, messageType)
			switch {
			case test.noOrigin:
				require.Empty(t, message)
			case test.origin == "same-host":
				require.Equal(t, fmt.Sprintf("http://127.0.0.1:%d", server.port), string(message))
			default:
				require.Equal(t, test.origin, string(message))
			}
			require.EqualValues(t, 1, called.Load())
		})
	}
}

func TestBindWebsocketHandshakeRejectionsRenderExpectedHTTPStatuses(t *testing.T) {
	var called atomic.Bool
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router)

	unsupportedVersion := sendWebsocketHandshakeRequest(t, server, "12", true)
	require.Equal(t, http.StatusBadRequest, unsupportedVersion.StatusCode)
	require.Equal(t, "13", unsupportedVersion.Header.Get("Sec-WebSocket-Version"))
	require.NotEqual(t, http.StatusSwitchingProtocols, unsupportedVersion.StatusCode)
	require.NoError(t, unsupportedVersion.Body.Close())

	missingUpgrade := sendWebsocketHandshakeRequest(t, server, "13", false)
	require.Equal(t, http.StatusUpgradeRequired, missingUpgrade.StatusCode)
	require.Equal(t, "websocket", missingUpgrade.Header.Get("Upgrade"))
	require.NotEqual(t, http.StatusSwitchingProtocols, missingUpgrade.StatusCode)
	require.NoError(t, missingUpgrade.Body.Close())
	require.False(t, called.Load())
}

func TestBindWebsocketRequiresManagedServerContextBeforeUpgrading(t *testing.T) {
	var called atomic.Bool
	recorder := serveUnmanagedWebsocketRequest(t, BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.NotEqual(t, http.StatusSwitchingProtocols, recorder.Code)
	require.Empty(t, recorder.Header().Get("Sec-WebSocket-Accept"))
	require.False(t, called.Load())
}

func TestBindWebsocketPreservesPermittedHandshakeHeaders(t *testing.T) {
	addHeaders := func(c *gin.Context) {
		header := c.Writer.Header()
		header.Add("Set-Cookie", "first=1; Path=/")
		header.Add("Set-Cookie", "second=2; Path=/")
		header.Set("X-Content-Security-Policy", "frame-ancestors 'none'")
		header.Set("Connection", "close, X-Remove-Me")
		header.Set("X-Remove-Me", "nominated")
		header.Set("Sec-WebSocket-Protocol", "middleware-owned")
		c.Next()
	}

	t.Run("successful upgrade filters protocol and connection-nominated fields", func(t *testing.T) {
		router := newWebsocketProtocolTestRouter()
		router.Use(addHeaders)
		router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
			return conn.Send([]byte("ready"))
		}))

		server := startWebsocketProtocolTestServer(t, router)
		client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		require.NoError(t, err)
		requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
		cleanupWebsocketClient(t, client)
		require.Equal(t, []string{"first=1; Path=/", "second=2; Path=/"}, response.Header.Values("Set-Cookie"))
		require.Equal(t, "frame-ancestors 'none'", response.Header.Get("X-Content-Security-Policy"))
		require.Empty(t, response.Header.Get("X-Remove-Me"))
		require.Empty(t, response.Header.Get("Sec-WebSocket-Protocol"))
		require.Equal(t, []string{"Upgrade"}, response.Header.Values("Connection"))

		require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
		messageType, message, err := client.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.TextMessage, messageType)
		require.Equal(t, "ready", string(message))
	})

	t.Run("pre-upgrade rejection retains original fields", func(t *testing.T) {
		var called atomic.Bool
		router := newWebsocketProtocolTestRouter()
		router.Use(addHeaders)
		router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
			called.Store(true)

			return nil
		}))

		server := startWebsocketProtocolTestServer(t, router)
		headers := http.Header{"Origin": {"https://attacker.example"}}
		_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), headers)
		require.Error(t, err)
		requireWebsocketStatus(t, response, http.StatusForbidden)
		require.Equal(t, []string{"first=1; Path=/", "second=2; Path=/"}, response.Header.Values("Set-Cookie"))
		require.Equal(t, "nominated", response.Header.Get("X-Remove-Me"))
		require.False(t, called.Load())
		require.NoError(t, response.Body.Close())
	})
}

func TestBindWebsocketHandlerFailureCloses1011WithoutRenderingHTTP(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	failure := errors.New("producer failed")
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		return failure
	}))

	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1011, closeErr.Code)
	require.Equal(t, "stream failed", closeErr.Text)
	require.NoError(t, server.stop(t))
	require.EqualValues(t, 1, logger.errorCalls.Load())
	require.NoError(t, client.Close())
}

func TestBindWebsocketPanicClosesBeforeRecoveryAndDoesNotRenderHTTP(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	router := gin.New()
	router.Use(ErrorMiddleware(), RecoveryWithSentry(logger))
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		panic(errors.New("producer panic"))
	}))

	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1011, closeErr.Code)
	require.Equal(t, "stream failed", closeErr.Text)
	require.NoError(t, server.stop(t))
	require.EqualValues(t, 1, logger.errorCalls.Load())
	require.NoError(t, client.Close())
}

func sendWebsocketHandshakeRequest(t *testing.T, server *websocketProtocolTestServer, version string, includeUpgrade bool) *http.Response {
	t.Helper()

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/socket", server.port),
		http.NoBody,
	)
	require.NoError(t, err)
	request.Header.Set("Connection", "Upgrade")
	if includeUpgrade {
		request.Header.Set("Upgrade", "websocket")
	}
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Version", version)

	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	require.NoError(t, err)

	return response
}

// newWebsocketProtocolTestRouter installs the ordinary HTTP error renderer before
// routes so failed upgrades exercise the managed server's negotiated error path.
func newWebsocketProtocolTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(ErrorMiddleware())

	return router
}

// startWebsocketProtocolTestServer exercises upgrades through a managed HttpServer,
// rather than Gin's recorder-based test helper.
func startWebsocketProtocolTestServer(t *testing.T, router *gin.Engine, configure ...func(*HttpServer)) *websocketProtocolTestServer {
	t.Helper()

	logger := logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t))

	return startWebsocketProtocolTestServerWithLogger(t, router, logger, configure...)
}

func startWebsocketProtocolTestServerWithLogger(
	t *testing.T,
	router *gin.Engine,
	logger log.Logger,
	configure ...func(*HttpServer),
) *websocketProtocolTestServer {
	t.Helper()

	tracer := tracingMocks.NewInstrumentor(t)
	tracer.EXPECT().HttpHandler(router).Return(router).Once()
	settings := &Settings{Port: "0"}
	settings.Timeout.Shutdown = time.Second

	server, err := NewWithInterfaces(t.Context(), logger, router, tracer, settings, shutdownTestMetricRecorder{})
	require.NoError(t, err)
	for _, configureServer := range configure {
		configureServer(server)
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

	t.Cleanup(func() {
		if err := fixture.stop(t); err != nil && !fixture.expectRunError {
			t.Error(err)
		}
	})

	return fixture
}

type websocketProtocolTestServer struct {
	server         *HttpServer
	port           int
	cancel         context.CancelFunc
	runDone        chan error
	runOnce        sync.Once
	runErr         error
	expectRunError bool
}

func (s *websocketProtocolTestServer) stop(t *testing.T) error {
	return s.stopWithin(t, 3*time.Second)
}

func (s *websocketProtocolTestServer) stopWithin(t *testing.T, timeout time.Duration) error {
	t.Helper()

	s.runOnce.Do(func() {
		s.cancel()
		select {
		case s.runErr = <-s.runDone:
		case <-time.After(timeout):
			s.runErr = errors.New("managed websocket test server did not stop")
		}
	})

	return s.runErr
}

func websocketTestURL(server *websocketProtocolTestServer, path string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d%s", server.port, path)
}

func newWebsocketCountingLogger(t *testing.T) *websocketCountingLogger {
	return &websocketCountingLogger{
		Logger: logMocks.NewLoggerMock(logMocks.WithMockAll, logMocks.WithTestingT(t)),
	}
}

type websocketCountingLogger struct {
	log.Logger
	errorCalls atomic.Int32
}

func (l *websocketCountingLogger) Error(ctx context.Context, message string, args ...any) {
	l.errorCalls.Add(1)
	l.Logger.Error(ctx, message, args...)
}

func readWebsocketClientClose(t *testing.T, client *websocket.Conn) *websocket.CloseError {
	t.Helper()

	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err := client.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)

	return closeErr
}

func requireWebsocketStatus(t *testing.T, response *http.Response, wantStatus int) {
	t.Helper()
	require.NotNil(t, response)
	require.Equal(t, wantStatus, response.StatusCode)
}

func serveUnmanagedWebsocketRequest(t *testing.T, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/socket", http.NoBody)
	router.ServeHTTP(recorder, request)

	return recorder
}

func TestBindWebsocketFailedHandshakeWriteNeverRendersHTTPError(t *testing.T) {
	var called atomic.Bool
	var responseWriter atomic.Pointer[websocketWriteCountingResponseWriter]
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		writer := &websocketWriteCountingResponseWriter{ResponseWriter: c.Writer}
		responseWriter.Store(writer)
		c.Writer = writer
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
		called.Store(true)

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router)
	_, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.Error(t, err)
	if response != nil {
		require.NotEqual(t, http.StatusSwitchingProtocols, response.StatusCode)
		require.NotEqual(t, http.StatusInternalServerError, response.StatusCode)
		require.NoError(t, response.Body.Close())
	}
	require.False(t, called.Load())
	writer := responseWriter.Load()
	require.NotNil(t, writer)
	require.Zero(t, writer.bodyWrites.Load())
	require.NoError(t, server.stop(t))
}

type websocketWriteCountingResponseWriter struct {
	gin.ResponseWriter
	bodyWrites atomic.Int32
}

func (w *websocketWriteCountingResponseWriter) Write(data []byte) (int, error) {
	w.bodyWrites.Add(1)

	return w.ResponseWriter.Write(data)
}

func (w *websocketWriteCountingResponseWriter) WriteString(data string) (int, error) {
	w.bodyWrites.Add(1)

	return w.ResponseWriter.WriteString(data)
}

func (w *websocketWriteCountingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	var (
		conn net.Conn
		rw   *bufio.ReadWriter
		err  error
	)
	if conn, rw, err = w.ResponseWriter.Hijack(); err != nil {
		return nil, nil, err
	}

	failed := &websocketHandshakeWriteFailureConn{Conn: conn}

	return failed, bufio.NewReadWriter(rw.Reader, bufio.NewWriter(failed)), nil
}

type websocketHandshakeWriteFailureConn struct {
	net.Conn
}

func (c *websocketHandshakeWriteFailureConn) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestWebsocketSuccessfulHandshakeAccountsForHTTP101(t *testing.T) {
	writer := &websocketTestMetricWriter{writes: make(chan metric.Data, 1)}
	recorder := &websocketRequestMetricRecorder{
		started:   make(chan struct{}, 1),
		completed: make(chan struct{}, 1),
	}
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		MetricMiddleware("websocket", c, writer, recorder)
	})
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		return conn.Send([]byte("accounted"))
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, http.StatusSwitchingProtocols)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	_, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, []byte("accounted"), message)

	var requestMetrics metric.Data
	select {
	case requestMetrics = <-writer.writes:
	case <-time.After(time.Second):
		t.Fatal("successful websocket request did not emit request metrics")
	}
	select {
	case <-recorder.started:
	default:
		t.Fatal("successful websocket request was not counted as started")
	}
	select {
	case <-recorder.completed:
	case <-time.After(time.Second):
		t.Fatal("successful websocket request was not counted as completed")
	}

	found101Metric := false
	for _, datum := range requestMetrics {
		if datum.MetricName == MetricHttpStatus+"1XXPerRoute" && datum.Dimensions["Path"] == "/socket" && datum.Value == 1 {
			found101Metric = true
		}
	}
	require.True(t, found101Metric, "successful WebSocket upgrade must be accounted as HTTP 101")
}

type websocketTestMetricWriter struct {
	writes chan metric.Data
}

func (w *websocketTestMetricWriter) Write(_ context.Context, data metric.Data) {
	w.writes <- data
}

func (w *websocketTestMetricWriter) GetPriority() int {
	return metric.PriorityLow
}

func (w *websocketTestMetricWriter) WriteOne(ctx context.Context, datum *metric.Datum) {
	w.Write(ctx, metric.Data{datum})
}

type websocketRequestMetricRecorder struct {
	started   chan struct{}
	completed chan struct{}
}

func (r *websocketRequestMetricRecorder) TrackRequestStarted(context.Context) {
	r.started <- struct{}{}
}

func (r *websocketRequestMetricRecorder) TrackRequestCompleted(context.Context) {
	r.completed <- struct{}{}
}

func (*websocketRequestMetricRecorder) TrackConnectionOpened(context.Context) {}
func (*websocketRequestMetricRecorder) TrackConnectionClosed(context.Context) {}

func (*websocketRequestMetricRecorder) Run(ctx context.Context) error {
	<-ctx.Done()

	return nil
}
