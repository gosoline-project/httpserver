package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWebsocketConnSerializesConcurrentTextAndBinarySends(t *testing.T) {
	type outbound struct {
		messageType WebsocketMessageType
		payload     []byte
	}
	messages := []outbound{
		{messageType: WebsocketTextMessage, payload: []byte("text-one")},
		{messageType: WebsocketBinaryMessage, payload: []byte{0, 1, 2}},
		{messageType: WebsocketTextMessage, payload: nil},
		{messageType: WebsocketBinaryMessage, payload: []byte{255, 0, 128}},
		{messageType: WebsocketTextMessage, payload: []byte("text-two")},
		{messageType: WebsocketBinaryMessage, payload: []byte("binary-three")},
	}

	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		var workers sync.WaitGroup
		errorsBySend := make(chan error, len(messages))
		for _, message := range messages {
			message := message
			workers.Add(1)
			go func() {
				defer workers.Done()
				var err error
				if message.messageType == WebsocketBinaryMessage {
					err = conn.SendBinary(message.payload)
				} else {
					err = conn.Send(message.payload)
				}
				errorsBySend <- err
			}()
		}
		workers.Wait()
		close(errorsBySend)
		for err := range errorsBySend {
			if err != nil {
				return fmt.Errorf("send concurrent websocket message: %w", err)
			}
		}

		return nil
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))

	want := make(map[string]int, len(messages))
	for _, message := range messages {
		want[websocketMessageKey(message.messageType, message.payload)]++
	}
	for range messages {
		messageType, payload, readErr := client.ReadMessage()
		require.NoError(t, readErr)
		key := websocketMessageKey(WebsocketMessageType(messageType), payload)
		require.Equal(t, 1, want[key], "unexpected or duplicate frame type=%d payload=%q", messageType, payload)
		want[key]--
	}
	for key, count := range want {
		require.Zero(t, count, "missing frame %q", key)
	}
}

func TestWebsocketConnRejectsConcurrentApplicationReaders(t *testing.T) {
	type readResult struct {
		messageType WebsocketMessageType
		payload     []byte
		err         error
	}
	type clientReadResult struct {
		messageType int
		payload     []byte
		err         error
	}

	firstReaderStarted := make(chan struct{})
	secondReadRequested := make(chan struct{})
	firstReaderResult := make(chan readResult, 1)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		if err := conn.Send([]byte("ready")); err != nil {
			return err
		}
		go func() {
			close(firstReaderStarted)
			messageType, payload, err := conn.ReadMessage()
			firstReaderResult <- readResult{messageType: messageType, payload: payload, err: err}
		}()

		<-secondReadRequested
		_, _, secondReadErr := conn.ReadMessage()
		first := <-firstReaderResult
		if secondReadErr == nil {
			return errors.New("concurrent application read was accepted")
		}
		if !strings.Contains(strings.ToLower(secondReadErr.Error()), "read") {
			return fmt.Errorf("concurrent read error lacks operation context: %w", secondReadErr)
		}
		if first.err != nil {
			return fmt.Errorf("the active application reader failed: %w", first.err)
		}
		if first.messageType != WebsocketTextMessage {
			return fmt.Errorf("unexpected accepted message type %d", first.messageType)
		}

		return conn.Send(first.payload)
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ready"), message)
	select {
	case <-firstReaderStarted:
	case <-time.After(time.Second):
		t.Fatal("application reader did not start")
	}

	pong := make(chan struct{}, 1)
	client.SetPongHandler(func(string) error {
		select {
		case pong <- struct{}{}:
		default:
		}

		return nil
	})
	clientRead := make(chan clientReadResult, 1)
	go func() {
		messageType, payload, err := client.ReadMessage()
		clientRead <- clientReadResult{messageType: messageType, payload: payload, err: err}
	}()
	require.NoError(t, client.WriteControl(websocket.PingMessage, []byte("probe"), time.Now().Add(time.Second)))
	select {
	case <-pong:
	case <-time.After(time.Second):
		t.Fatal("active server reader did not process the ping before the competing read")
	}
	secondReadRequested <- struct{}{}
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("only-one-reader")))
	select {
	case result := <-clientRead:
		require.NoError(t, result.err)
		require.Equal(t, websocket.TextMessage, result.messageType)
		require.Equal(t, []byte("only-one-reader"), result.payload)
	case <-time.After(time.Second):
		t.Fatal("exclusive reader did not return the inbound frame")
	}
}

func TestWebsocketConnRejectModeProcessesControlFramesAndRejectsDataHeader(t *testing.T) {
	const rejectReason = "server stream does not accept messages"
	ready := make(chan struct{})
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		if err := conn.RejectIncomingMessages(rejectReason); err != nil {
			return err
		}
		if err := conn.RejectIncomingMessages("second reader must not start"); err == nil {
			return errors.New("second reject reader was accepted")
		}
		close(ready)
		<-ctx.Done()

		return ctx.Err()
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("reject-mode reader did not start")
	}

	pong := make(chan struct{}, 1)
	client.SetPongHandler(func(string) error {
		select {
		case pong <- struct{}{}:
		default:
		}

		return nil
	})
	require.NoError(t, writeMaskedWebsocketFrame(client.UnderlyingConn(), true, websocket.PingMessage, nil))
	require.NoError(t, writeMaskedWebsocketFrameHeader(client.UnderlyingConn(), true, websocket.TextMessage, 12))

	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1008, closeErr.Code)
	require.Equal(t, rejectReason, closeErr.Text)
	select {
	case <-pong:
	default:
		t.Fatal("reject-mode reader did not process the preceding ping")
	}
}

func TestWebsocketConnRejectInvalidReasonLeavesReadModeAvailable(t *testing.T) {
	invalidReasons := []string{strings.Repeat("x", 124), string([]byte{0xff})}
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		for _, reason := range invalidReasons {
			if err := conn.RejectIncomingMessages(reason); err == nil {
				return errors.New("invalid reject reason was accepted")
			}
		}
		if err := conn.Send([]byte("ready")); err != nil {
			return err
		}
		if messageType, payload, err := conn.ReadMessage(); err != nil {
			return err
		} else {
			if messageType != WebsocketTextMessage {
				return fmt.Errorf("unexpected message type %d", messageType)
			}

			return conn.Send(payload)
		}
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ready"), message)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("still readable")))
	messageType, message, err = client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("still readable"), message)
}

func TestWebsocketConnRejectModeCannotStartAfterApplicationRead(t *testing.T) {
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		var messageType WebsocketMessageType
		var first, second []byte
		var err error
		if err = conn.Send([]byte("ready")); err != nil {
			return fmt.Errorf("send read-mode greeting: %w", err)
		}
		if messageType, first, err = conn.ReadMessage(); err != nil {
			return fmt.Errorf("read first application message: %w", err)
		}
		if messageType != WebsocketTextMessage {
			return fmt.Errorf("unexpected first message type %d", messageType)
		}
		if err = conn.RejectIncomingMessages("must not compete with application reader"); err == nil {
			return errors.New("reject mode started after application reading began")
		}
		if err = conn.Send(append([]byte("first:"), first...)); err != nil {
			return fmt.Errorf("echo first application message: %w", err)
		}
		if messageType, second, err = conn.ReadMessage(); err != nil {
			return fmt.Errorf("read second application message: %w", err)
		}
		if messageType != WebsocketTextMessage {
			return fmt.Errorf("unexpected second message type %d", messageType)
		}

		return conn.Send(append([]byte("second:"), second...))
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ready"), message)

	for _, input := range []string{"one", "two"} {
		require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(input)))
		messageType, message, err = client.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.TextMessage, messageType)
		prefix := "first:"
		if input == "two" {
			prefix = "second:"
		}
		require.Equal(t, []byte(prefix+input), message)
	}
}

func TestWebsocketConnInvalidCloseLeavesConnectionUsable(t *testing.T) {
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		invalidClosures := []struct {
			code   int
			reason string
		}{
			{code: 1006, reason: "reserved"},
			{code: 1000, reason: strings.Repeat("x", 124)},
			{code: 1000, reason: string([]byte{0xff})},
		}
		for _, invalid := range invalidClosures {
			if err := conn.CloseWithStatus(invalid.code, invalid.reason); err == nil {
				return fmt.Errorf("invalid close code %d and reason were accepted", invalid.code)
			}
		}
		if err := conn.Send([]byte("still usable")); err != nil {
			return err
		}
		if _, message, err := conn.ReadMessage(); err != nil {
			return err
		} else {
			return conn.Send(message)
		}
	}))

	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("still usable"), message)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("ack")))
	messageType, message, err = client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("ack"), message)
}

func TestWebsocketConnCloseIsIdempotentAndFirstStatusWins(t *testing.T) {
	t.Run("normal close is idempotent", func(t *testing.T) {
		router := newWebsocketProtocolTestRouter()
		router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
			if err := conn.Close(); err != nil {
				return err
			}

			return conn.Close()
		}))
		server := startWebsocketProtocolTestServer(t, router)
		client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		require.NoError(t, err)
		requireWebsocketStatus(t, response, 101)
		cleanupWebsocketClient(t, client)
		closeErr := readWebsocketClientClose(t, client)
		require.Equal(t, 1000, closeErr.Code)
	})

	t.Run("selected application close survives binder cleanup", func(t *testing.T) {
		router := newWebsocketProtocolTestRouter()
		router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
			return conn.CloseWithStatus(1008, "policy selected")
		}))
		server := startWebsocketProtocolTestServer(t, router)
		client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
		require.NoError(t, err)
		requireWebsocketStatus(t, response, 101)
		cleanupWebsocketClient(t, client)
		closeErr := readWebsocketClientClose(t, client)
		require.Equal(t, 1008, closeErr.Code)
		require.Equal(t, "policy selected", closeErr.Text)
	})
}

func TestWebsocketConnReadLimitClosesOversizedFragmentedMessageWith1009(t *testing.T) {
	options := defaultWebsocketOptions()
	options.ReadLimit = 8
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(_ context.Context, conn *WebsocketConn) error {
		if _, _, err := conn.ReadMessage(); err != nil {
			return err
		}

		return nil
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		server.websockets.options = options
	})

	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)
	raw := client.UnderlyingConn()
	require.NoError(t, writeMaskedWebsocketFrame(raw, false, websocket.TextMessage, []byte("1234")))
	require.NoError(t, writeMaskedWebsocketFrame(raw, true, 0, []byte("56789")))

	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1009, closeErr.Code)
}

func TestWebsocketExpectedErrorClassificationInspectsEveryWrappedCause(t *testing.T) {
	expected := fmt.Errorf("outer: %w", errors.Join(context.Canceled, fmt.Errorf("peer: %w", ErrClientDisconnected)))
	require.True(t, websocketExpectedError(expected))

	failure := &websocketTestFailure{message: "producer failed"}
	mixed := fmt.Errorf("outer: %w", errors.Join(context.Canceled, fmt.Errorf("producer: %w", failure)))
	require.False(t, websocketExpectedError(mixed))
	require.ErrorIs(t, mixed, failure)
	var gotFailure *websocketTestFailure
	require.ErrorAs(t, mixed, &gotFailure)
	require.Same(t, failure, gotFailure)
}

func TestWebsocketHandlerErrorTreesSelectCloseAndLoggingPolicy(t *testing.T) {
	failure := &websocketTestFailure{message: "producer failed"}
	cases := []struct {
		name      string
		err       error
		closeCode int
		closeText string
		logCount  int32
	}{
		{
			name:      "only expected cancellation leaves",
			err:       fmt.Errorf("joined peer close: %w", errors.Join(context.Canceled, ErrClientDisconnected)),
			closeCode: 1000,
			logCount:  0,
		},
		{
			name:      "cancellation joined with application failure",
			err:       fmt.Errorf("joined producer failure: %w", errors.Join(context.Canceled, failure)),
			closeCode: 1011,
			closeText: "stream failed",
			logCount:  1,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			logger := newWebsocketCountingLogger(t)
			router := newWebsocketProtocolTestRouter()
			router.GET("/socket", BindWebsocketN(func(context.Context, *WebsocketConn) error {
				return test.err
			}))
			server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
			client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
			require.NoError(t, err)
			requireWebsocketStatus(t, response, 101)
			closeErr := readWebsocketClientClose(t, client)
			require.Equal(t, test.closeCode, closeErr.Code)
			require.Equal(t, test.closeText, closeErr.Text)
			require.NoError(t, server.stop(t))
			require.Equal(t, test.logCount, logger.errorCalls.Load())
			require.NoError(t, client.Close())
		})
	}
}

func TestWebsocketPeerCloseCancelsCallbackWithoutApplicationFailureLog(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	callbackDone := make(chan error, 1)
	contextErr := make(chan error, 1)
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		_, _, err := conn.ReadMessage()
		contextErr <- ctx.Err()
		callbackDone <- err

		return err
	}))
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)

	deadline := time.Now().Add(time.Second)
	require.NoError(t, client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""), deadline))
	select {
	case err := <-contextErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("peer close did not cancel callback context")
	}
	select {
	case err := <-callbackDone:
		require.ErrorIs(t, err, ErrClientDisconnected)
		var closeErr *websocket.CloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, 1000, closeErr.Code)
	case <-time.After(time.Second):
		t.Fatal("peer close did not unblock the application reader")
	}
	require.NoError(t, server.stop(t))
	require.Zero(t, logger.errorCalls.Load())
}

func TestWebsocketRequestContextCancellationUnblocksReaderWithoutSelfJoin(t *testing.T) {
	logger := newWebsocketCountingLogger(t)
	cancelRequest := make(chan context.CancelFunc, 1)
	callbackDone := make(chan struct {
		contextErr error
		readErr    error
	}, 1)
	router := newWebsocketProtocolTestRouter()
	router.Use(func(c *gin.Context) {
		requestContext, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(requestContext)
		cancelRequest <- cancel
		c.Next()
	})
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		_, _, readErr := conn.ReadMessage()
		callbackDone <- struct {
			contextErr error
			readErr    error
		}{contextErr: ctx.Err(), readErr: readErr}

		return readErr
	}))
	server := startWebsocketProtocolTestServerWithLogger(t, router, logger)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)

	select {
	case cancel := <-cancelRequest:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("managed request context was not attached")
	}
	select {
	case result := <-callbackDone:
		require.ErrorIs(t, result.contextErr, context.Canceled)
		require.ErrorIs(t, result.readErr, ErrClientDisconnected)
	case <-time.After(time.Second):
		t.Fatal("request context cancellation did not close the owned socket and unblock reading")
	}
	require.NoError(t, server.stop(t))
	require.Zero(t, logger.errorCalls.Load())
}

func TestWebsocketRejectModePreservesProtocolErrorClose1002(t *testing.T) {
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		if err := conn.RejectIncomingMessages("server stream does not accept messages"); err != nil {
			return err
		}
		<-ctx.Done()

		return ctx.Err()
	}))
	server := startWebsocketProtocolTestServer(t, router)
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)

	// Client data frames must be masked. The malformed header must retain the
	// driver's protocol-error close instead of the application's 1008 policy.
	require.NoError(t, writeWebsocketBytes(client.UnderlyingConn(), []byte{0x81, 0x00}))
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1002, closeErr.Code)
}

func TestWebsocketRejectModePreservesReadLimitClose1009(t *testing.T) {
	options := defaultWebsocketOptions()
	options.ReadLimit = 8
	router := newWebsocketProtocolTestRouter()
	router.GET("/socket", BindWebsocketN(func(ctx context.Context, conn *WebsocketConn) error {
		if err := conn.RejectIncomingMessages("server stream does not accept messages"); err != nil {
			return err
		}
		<-ctx.Done()

		return ctx.Err()
	}))
	server := startWebsocketProtocolTestServer(t, router, func(server *HttpServer) {
		server.websockets.options = options
	})
	client, response, err := websocket.DefaultDialer.DialContext(t.Context(), websocketTestURL(server, "/socket"), nil)
	require.NoError(t, err)
	requireWebsocketStatus(t, response, 101)
	cleanupWebsocketClient(t, client)

	// An oversized valid data-frame header is rejected before application policy,
	// even though its fragmented body has not arrived.
	require.NoError(t, writeMaskedWebsocketFrameHeader(client.UnderlyingConn(), true, websocket.TextMessage, 9))
	closeErr := readWebsocketClientClose(t, client)
	require.Equal(t, 1009, closeErr.Code)
}

func websocketMessageKey(messageType WebsocketMessageType, payload []byte) string {
	return fmt.Sprintf("%d:%q", messageType, payload)
}

func writeMaskedWebsocketFrame(conn net.Conn, final bool, opcode int, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("test frame payload %d exceeds short-frame limit", len(payload))
	}

	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	first := byte(opcode)
	if final {
		first |= 0x80
	}
	frame := make([]byte, 2+len(mask)+len(payload))
	frame[0] = first
	frame[1] = 0x80 | byte(len(payload))
	copy(frame[2:], mask[:])
	for i, value := range payload {
		frame[2+len(mask)+i] = value ^ mask[i%len(mask)]
	}

	return writeWebsocketBytes(conn, frame)
}

func writeMaskedWebsocketFrameHeader(conn net.Conn, final bool, opcode int, payloadLength int) error {
	if payloadLength > 125 {
		return fmt.Errorf("test frame payload %d exceeds short-frame limit", payloadLength)
	}

	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	first := byte(opcode)
	if final {
		first |= 0x80
	}
	header := []byte{first, 0x80 | byte(payloadLength), mask[0], mask[1], mask[2], mask[3]}

	return writeWebsocketBytes(conn, header)
}

func writeWebsocketBytes(conn net.Conn, data []byte) error {
	for len(data) != 0 {
		if written, err := conn.Write(data); err != nil {
			return err
		} else if written == 0 {
			return errors.New("websocket test frame write made no progress")
		} else {
			data = data[written:]
		}
	}

	return nil
}

type websocketTestFailure struct {
	message string
}

func (f *websocketTestFailure) Error() string {
	return f.message
}
