package httpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/gorilla/websocket"
	"github.com/justtrackio/gosoline/pkg/refl"
)

// BindWebsocket adapts a typed WebSocket handler into a Gin handler by binding
// request data before upgrading the connection.
func BindWebsocket[I any](handler func(context.Context, *I, *WebsocketConn) error, binders ...binding.Binding) gin.HandlerFunc {
	return BindWebsocketR[I](func(ctx context.Context, _ *http.Request, input *I, conn *WebsocketConn) error {
		return handler(ctx, input, conn)
	}, binders...)
}

// BindWebsocketR adapts a typed WebSocket handler like BindWebsocket, and also
// passes the raw HTTP request to the handler.
func BindWebsocketR[I any](handler func(context.Context, *http.Request, *I, *WebsocketConn) error, binders ...binding.Binding) gin.HandlerFunc {
	tags := refl.GetTagNames(new(I))

	return func(ginCtx *gin.Context) {
		var input *I
		var err error
		if input, err = BindHandleRequest[I](ginCtx, tags, binders); err != nil {
			reportGinErrorWithType(ginCtx, NewErrorWithStatus(http.StatusBadRequest, fmt.Errorf("bind websocket request: %w", err)), gin.ErrorTypeBind)

			return
		}

		bindWebsocketConnection(ginCtx, func(ctx context.Context, request *http.Request, conn *WebsocketConn) error {
			return handler(ctx, request, input, conn)
		})
	}
}

// BindWebsocketN adapts a WebSocket handler that does not need request input
// binding.
func BindWebsocketN(handler func(context.Context, *WebsocketConn) error) gin.HandlerFunc {
	return BindWebsocketNR(func(ctx context.Context, _ *http.Request, conn *WebsocketConn) error {
		return handler(ctx, conn)
	})
}

// BindWebsocketNR adapts a WebSocket handler that does not need request input
// binding, but still needs access to the raw HTTP request.
func BindWebsocketNR(handler func(context.Context, *http.Request, *WebsocketConn) error) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		bindWebsocketConnection(ginCtx, handler)
	}
}

func bindWebsocketConnection(ginCtx *gin.Context, handler func(context.Context, *http.Request, *WebsocketConn) error) {
	registry := websocketRegistryFromContext(ginCtx.Request.Context())
	if registry == nil {
		reportWebsocketHTTPError(ginCtx, http.StatusInternalServerError, errors.New("websocket route requires an httpserver-managed server"))

		return
	}

	reservation, accepted := registry.reserve()
	if !accepted {
		reportWebsocketHTTPError(ginCtx, http.StatusServiceUnavailable, errors.New("websocket server is shutting down"))

		return
	}
	defer reservation.release()

	upgrade := upgradeWebsocketConnection(ginCtx, registry, reservation)
	if upgrade.err != nil {
		handleWebsocketUpgradeFailure(ginCtx, registry, upgrade)

		return
	}

	websocketConn, callbackCtx := newWebsocketConn(ginCtx, ginCtx.Request.Context(), upgrade.conn, registry.options)
	defer websocketConn.finalize()
	defer ginCtx.Abort()

	if !reservation.promote(websocketConn) {
		closeWebsocketBestEffort(registry, ginCtx.Request.Context(), websocketConn, websocket.CloseGoingAway, "server shutting down", "close websocket after shutdown race")

		return
	}

	handlerErr := invokeWebsocketHandler(registry, ginCtx.Request.Context(), callbackCtx, ginCtx.Request, websocketConn, handler)
	failure := closeWebsocketAfterHandler(websocketConn, handlerErr)
	if failure != nil && !websocketExpectedError(failure) {
		registry.logger.Error(ginCtx.Request.Context(), "%w", failure)
	}
}

type websocketUpgradeResult struct {
	conn           *websocket.Conn
	responseWriter *websocketHijackResponseWriter
	status         int
	err            error
}

func upgradeWebsocketConnection(ginCtx *gin.Context, registry *websocketRegistry, reservation *websocketReservation) websocketUpgradeResult {
	var result websocketUpgradeResult
	upgrader := websocket.Upgrader{
		HandshakeTimeout: registry.options.WriteTimeout,
		CheckOrigin:      registry.options.CheckOrigin,
		Error: func(writer http.ResponseWriter, _ *http.Request, status int, _ error) {
			writer.Header().Set("Sec-WebSocket-Version", "13")
			result.status = status
		},
	}

	originalWriter := ginCtx.Writer
	result.responseWriter = &websocketHijackResponseWriter{
		ResponseWriter: originalWriter,
		ginCtx:         ginCtx,
		reservation:    reservation,
	}
	responseHeaders := filteredWebsocketResponseHeaders(originalWriter.Header())
	ginCtx.Status(http.StatusSwitchingProtocols)

	func() {
		ginCtx.Writer = result.responseWriter
		defer func() {
			ginCtx.Writer = originalWriter
		}()

		result.conn, result.err = upgrader.Upgrade(result.responseWriter, ginCtx.Request, responseHeaders)
	}()

	return result
}

func handleWebsocketUpgradeFailure(ginCtx *gin.Context, registry *websocketRegistry, upgrade websocketUpgradeResult) {
	if upgrade.responseWriter.hijackRejected {
		ginCtx.Abort()

		return
	}

	if !httpUnavailable(ginCtx) {
		reportPreHijackWebsocketFailure(ginCtx, upgrade)

		return
	}

	logPostHijackWebsocketFailure(ginCtx, registry, upgrade)
	ginCtx.Abort()
}

func reportPreHijackWebsocketFailure(ginCtx *gin.Context, upgrade websocketUpgradeResult) {
	status := upgrade.status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	cause := upgrade.err
	if upgrade.responseWriter.hijackErr != nil {
		cause = upgrade.responseWriter.hijackErr
	}
	reportWebsocketHTTPError(ginCtx, status, fmt.Errorf("upgrade websocket handshake: %w", cause))
}

func logPostHijackWebsocketFailure(ginCtx *gin.Context, registry *websocketRegistry, upgrade websocketUpgradeResult) {
	cause := upgrade.err
	operation := "write websocket handshake"
	if !upgrade.responseWriter.hijackAttached && upgrade.responseWriter.hijackErr != nil {
		cause = upgrade.responseWriter.hijackErr
		operation = "hijack websocket response"
	}
	if websocketExpectedError(cause) {
		return
	}

	failure := fmt.Errorf("%s: %w", operation, cause)
	registry.logger.Error(ginCtx.Request.Context(), "%w", failure)
}

func invokeWebsocketHandler(registry *websocketRegistry, requestCtx context.Context, callbackCtx context.Context, request *http.Request, conn *WebsocketConn, handler func(context.Context, *http.Request, *WebsocketConn) error) error {
	defer func() {
		if recovered := recover(); recovered != nil {
			closeWebsocketBestEffort(registry, requestCtx, conn, websocket.CloseInternalServerErr, "stream failed", "close websocket after handler panic")
			panic(recovered)
		}
	}()

	return handler(callbackCtx, request, conn)
}

func closeWebsocketAfterHandler(conn *WebsocketConn, handlerErr error) error {
	switch {
	case handlerErr == nil:
		if closeErr := conn.Close(); closeErr != nil && !websocketExpectedError(closeErr) {
			return fmt.Errorf("close websocket after handler return: %w", closeErr)
		}
	case websocketExpectedError(handlerErr):
		if closeErr := conn.Close(); closeErr != nil && !websocketExpectedError(closeErr) {
			return fmt.Errorf("close websocket after expected handler exit: %w", closeErr)
		}
	default:
		failure := fmt.Errorf("websocket handler failed: %w", handlerErr)
		if closeErr := conn.CloseWithStatus(websocket.CloseInternalServerErr, "stream failed"); closeErr != nil && !websocketExpectedError(closeErr) {
			return errors.Join(failure, fmt.Errorf("close websocket after handler failure: %w", closeErr))
		}

		return failure
	}

	return nil
}

func closeWebsocketBestEffort(registry *websocketRegistry, requestCtx context.Context, conn *WebsocketConn, code int, reason string, operation string) {
	if err := conn.CloseWithStatus(code, reason); err != nil && !websocketExpectedError(err) {
		registry.logger.Debug(requestCtx, "%s: %w", operation, err)
	}
}

func reportWebsocketHTTPError(ginCtx *gin.Context, status int, err error) {
	reportGinErrorWithType(ginCtx, NewErrorWithStatus(status, err), gin.ErrorTypePrivate)
}

type websocketHijackResponseWriter struct {
	gin.ResponseWriter
	ginCtx      *gin.Context
	reservation *websocketReservation

	hijackErr      error
	hijackAttached bool
	hijackRejected bool
}

func (w *websocketHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	var conn net.Conn
	var buffered *bufio.ReadWriter
	var err error
	if conn, buffered, err = w.ResponseWriter.Hijack(); err != nil {
		w.hijackErr = fmt.Errorf("hijack websocket response: %w", err)
		if w.Written() {
			markHttpUnavailable(w.ginCtx)
		}

		return nil, nil, w.hijackErr
	}

	markHttpUnavailable(w.ginCtx)
	if !w.reservation.attachRaw(conn) {
		w.hijackRejected = true
		w.hijackErr = errors.New("websocket registry stopped before socket attachment")

		return nil, nil, w.hijackErr
	}
	w.hijackAttached = true

	return conn, buffered, nil
}

var websocketExcludedResponseHeaders = [...]string{
	"Connection",
	"Upgrade",
	"Keep-Alive",
	"Proxy-Connection",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Content-Length",
	"Content-Type",
	"Content-Encoding",
}

func filteredWebsocketResponseHeaders(headers http.Header) http.Header {
	var filtered http.Header
	for name, values := range headers {
		if websocketResponseHeaderExcluded(name, headers) {
			continue
		}
		if filtered == nil {
			filtered = make(http.Header)
		}
		filtered[name] = values
	}

	return filtered
}

func websocketResponseHeaderExcluded(name string, headers http.Header) bool {
	return websocketProtocolHeader(name) || websocketHopByHopHeader(name) || connectionHeaderNominates(name, headers)
}

func websocketProtocolHeader(name string) bool {
	const secWebsocketPrefix = "Sec-WebSocket-"

	return len(name) >= len(secWebsocketPrefix) && strings.EqualFold(name[:len(secWebsocketPrefix)], secWebsocketPrefix)
}

func websocketHopByHopHeader(name string) bool {
	for _, excluded := range websocketExcludedResponseHeaders {
		if strings.EqualFold(name, excluded) {
			return true
		}
	}

	return false
}

func connectionHeaderNominates(name string, headers http.Header) bool {
	for key, values := range headers {
		if strings.EqualFold(key, "Connection") && connectionHeaderValuesContainToken(values, name) {
			return true
		}
	}

	return false
}

func connectionHeaderValuesContainToken(values []string, name string) bool {
	for _, value := range values {
		start := 0
		for start <= len(value) {
			end := strings.IndexByte(value[start:], ',')
			segmentEnd := len(value)
			if end >= 0 {
				segmentEnd = start + end
			}
			token := strings.TrimSpace(value[start:segmentEnd])
			if token != "" && strings.EqualFold(name, token) {
				return true
			}
			if end < 0 {
				break
			}
			start = segmentEnd + 1
		}
	}

	return false
}
