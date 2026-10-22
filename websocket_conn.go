package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// WebsocketMessageType identifies the kind of WebSocket data message returned
// by ReadMessage.
type WebsocketMessageType int

const (
	// WebsocketTextMessage is a UTF-8 text message.
	WebsocketTextMessage WebsocketMessageType = 1
	// WebsocketBinaryMessage is a binary message.
	WebsocketBinaryMessage WebsocketMessageType = 2
)

type websocketReadMode uint8

const (
	websocketReadModeUndecided websocketReadMode = iota
	websocketReadModeApplication
	websocketReadModeReject
)

// WebsocketConn is a bidirectional WebSocket connection owned by an
// httpserver-managed request.
type WebsocketConn struct {
	conn    *websocket.Conn
	options WebsocketOptions
	ctx     context.Context
	cancel  context.CancelFunc

	writeMu sync.Mutex

	readMu       sync.Mutex
	readMode     websocketReadMode
	readActive   bool
	readTerminal error
	rejectDone   chan struct{}

	closeMu          sync.Mutex
	closeSelected    bool
	closeCode        int
	closeReason      string
	closeAttemptDone chan struct{}

	stopRequestCancellation func() bool
	requestCancellationDone chan struct{}
}

func newWebsocketConn(parent context.Context, requestCtx context.Context, conn *websocket.Conn, options WebsocketOptions) (*WebsocketConn, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	websocketConn := &WebsocketConn{
		conn:                    conn,
		options:                 options,
		ctx:                     ctx,
		cancel:                  cancel,
		requestCancellationDone: make(chan struct{}),
	}
	conn.SetReadLimit(options.ReadLimit)
	websocketConn.stopRequestCancellation = context.AfterFunc(requestCtx, func() {
		defer close(websocketConn.requestCancellationDone)
		websocketConn.terminateTransport()
	})

	return websocketConn, ctx
}

// ReadMessage reads one complete text or binary message. Only one application
// reader may call ReadMessage at a time. Control frames are handled by the
// underlying WebSocket transport.
func (c *WebsocketConn) ReadMessage() (WebsocketMessageType, []byte, error) {
	var (
		messageType int
		data        []byte
		err         error
	)
	c.readMu.Lock()
	switch c.readMode {
	case websocketReadModeReject:
		c.readMu.Unlock()

		return 0, nil, errors.New("read websocket message: incoming messages are rejected")
	case websocketReadModeUndecided:
		c.readMode = websocketReadModeApplication
	}
	if c.readActive {
		c.readMu.Unlock()

		return 0, nil, errors.New("read websocket message: concurrent reads are not supported")
	}
	if c.readTerminal != nil {
		err := c.readTerminal
		c.readMu.Unlock()

		return 0, nil, err
	}
	c.readActive = true
	ctxErr := c.ctx.Err()
	c.readMu.Unlock()

	if ctxErr != nil {
		err = websocketOperationError("read websocket message", ctxErr)
		c.finishReadError(err)
		c.terminateTransport()

		return 0, nil, err
	}

	if messageType, data, err = c.conn.ReadMessage(); err != nil {
		driverErr := err
		err = websocketOperationError("read websocket message", driverErr)
		c.finishReadError(err)
		c.terminateAfterReadError(driverErr)

		return 0, nil, err
	}

	c.readMu.Lock()
	c.readActive = false
	c.readMu.Unlock()

	switch messageType {
	case websocket.TextMessage:
		return WebsocketTextMessage, data, nil
	case websocket.BinaryMessage:
		return WebsocketBinaryMessage, data, nil
	default:
		err = fmt.Errorf("read websocket message: unsupported message type %d", messageType)
		c.finishReadError(err)
		c.terminateTransport()

		return 0, nil, err
	}
}

// Send sends data as one text message. A nil or empty slice sends an empty
// text message.
func (c *WebsocketConn) Send(data []byte) error {
	return c.send(websocket.TextMessage, data)
}

// SendBinary sends data as one binary message.
func (c *WebsocketConn) SendBinary(data []byte) error {
	return c.send(websocket.BinaryMessage, data)
}

func (c *WebsocketConn) send(messageType int, data []byte) error {
	if err := c.ctx.Err(); err != nil {
		return websocketOperationError("send websocket message", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.ctx.Err(); err != nil {
		return websocketOperationError("send websocket message", err)
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.options.WriteTimeout)); err != nil {
		wrappedErr := fmt.Errorf("set websocket write deadline: %w", err)
		c.terminateTransport()

		return wrappedErr
	}
	if err := c.conn.WriteMessage(messageType, data); err != nil {
		wrappedErr := websocketOperationError("send websocket message", err)
		c.terminateTransport()

		return wrappedErr
	}

	return nil
}

// Close sends a normal closure and terminates the connection. The first close
// status selected for the connection is retained across concurrent cleanup.
func (c *WebsocketConn) Close() error {
	return c.CloseWithStatus(websocket.CloseNormalClosure, "")
}

// CloseWithStatus sends a validated server close status and reason, then
// terminates the connection. Invalid input leaves the connection usable.
func (c *WebsocketConn) CloseWithStatus(code int, reason string) error {
	return c.closeWithDeadlineSelected(code, reason, time.Now().Add(c.options.CloseTimeout), false)
}

// RejectIncomingMessages makes the connection outbound-only. Its framework
// reader continues to process control frames and closes with status 1008 as
// soon as a valid data-message header is received. Frame validation and the
// configured read limit take precedence over this policy.
func (c *WebsocketConn) RejectIncomingMessages(reason string) error {
	if err := validateWebsocketClose(websocket.ClosePolicyViolation, reason); err != nil {
		return fmt.Errorf("reject incoming websocket messages: %w", err)
	}

	c.readMu.Lock()
	if c.readMode != websocketReadModeUndecided {
		c.readMu.Unlock()

		return errors.New("reject incoming websocket messages: reading has already started or rejection is already enabled")
	}
	c.readMode = websocketReadModeReject
	c.rejectDone = make(chan struct{})
	done := c.rejectDone
	c.readMu.Unlock()

	go c.rejectIncomingMessages(done, reason)

	return nil
}

func (c *WebsocketConn) rejectIncomingMessages(done chan struct{}, reason string) {
	defer close(done)

	if err := c.ctx.Err(); err != nil {
		c.terminateAfterReadError(err)

		return
	}

	var messageType int
	var err error
	if messageType, _, err = c.conn.NextReader(); err != nil {
		c.terminateAfterReadError(err)

		return
	}
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		c.terminateAfterReadError(fmt.Errorf("websocket reader returned unsupported message type %d", messageType))

		return
	}

	//nolint:errcheck // The policy close is best effort; closeWithDeadline always closes the transport and cancels its context.
	_ = c.closeWithDeadline(websocket.ClosePolicyViolation, reason, time.Now().Add(c.options.CloseTimeout))
}

func (c *WebsocketConn) closeWithDeadline(code int, reason string, deadline time.Time) error {
	return c.closeWithDeadlineSelected(code, reason, deadline, true)
}

func (c *WebsocketConn) closeWithDeadlineSelected(code int, reason string, deadline time.Time, hardCloseOnRepeat bool) error {
	if err := validateWebsocketClose(code, reason); err != nil {
		return fmt.Errorf("close websocket with status: %w", err)
	}

	c.closeMu.Lock()
	if c.closeSelected {
		c.closeMu.Unlock()
		if hardCloseOnRepeat {
			c.terminateTransport()
		}

		return nil
	}
	c.closeSelected = true
	c.closeCode = code
	c.closeReason = reason
	attemptDone := make(chan struct{})
	c.closeAttemptDone = attemptDone
	c.closeMu.Unlock()

	controlErr := c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
	transportErr := c.conn.Close()
	c.cancel()
	close(attemptDone)

	var closeErr error
	if controlErr != nil {
		closeErr = websocketOperationError("write websocket close control", controlErr)
	}
	if transportErr != nil {
		transportCloseErr := websocketOperationError("close websocket transport", transportErr)
		if closeErr == nil {
			closeErr = transportCloseErr
		} else {
			closeErr = errors.Join(closeErr, transportCloseErr)
		}
	}

	return closeErr
}

func (c *WebsocketConn) rememberDriverClose(code int) {
	c.closeMu.Lock()
	if !c.closeSelected {
		c.closeSelected = true
		c.closeCode = code
	}
	c.closeMu.Unlock()
}

func (c *WebsocketConn) finishReadError(err error) {
	c.readMu.Lock()
	c.readActive = false
	if c.readTerminal == nil {
		c.readTerminal = err
	}
	c.readMu.Unlock()
}

func (c *WebsocketConn) terminateAfterReadError(err error) {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		switch {
		case errors.Is(err, websocket.ErrReadLimit):
			c.rememberDriverClose(websocket.CloseMessageTooBig)
		case strings.HasPrefix(err.Error(), "websocket: "):
			c.rememberDriverClose(websocket.CloseProtocolError)
		}
	}
	c.terminateTransport()
}

func (c *WebsocketConn) terminateTransport() {
	//nolint:errcheck // Terminal cleanup is best effort; cancellation must still run if the socket close reports an error.
	_ = c.conn.Close()
	c.cancel()
}

func (c *WebsocketConn) finalize() {
	c.terminateTransport()

	c.closeMu.Lock()
	closeAttemptDone := c.closeAttemptDone
	c.closeMu.Unlock()
	if closeAttemptDone != nil {
		<-closeAttemptDone
	}

	if c.stopRequestCancellation != nil && !c.stopRequestCancellation() {
		<-c.requestCancellationDone
	}

	c.readMu.Lock()
	rejectDone := c.rejectDone
	c.readMu.Unlock()
	if rejectDone != nil {
		<-rejectDone
	}
}

func validateWebsocketClose(code int, reason string) error {
	if !validWebsocketCloseCode(code) {
		return fmt.Errorf("status code %d is not valid for a server close", code)
	}
	if !utf8.ValidString(reason) {
		return errors.New("reason must be valid UTF-8")
	}
	if len(reason) > 123 {
		return fmt.Errorf("reason is %d bytes; maximum is 123", len(reason))
	}

	return nil
}

func validWebsocketCloseCode(code int) bool {
	switch code {
	case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseProtocolError, websocket.CloseUnsupportedData,
		websocket.CloseInvalidFramePayloadData, websocket.ClosePolicyViolation, websocket.CloseMessageTooBig,
		websocket.CloseInternalServerErr, websocket.CloseServiceRestart, websocket.CloseTryAgainLater,
		1014:
		return true
	default:
		return code >= 3000 && code <= 4999
	}
}

func websocketOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if websocketExpectedError(err) {
		return fmt.Errorf("%s: %w: %w", operation, ErrClientDisconnected, err)
	}

	return fmt.Errorf("%s: %w", operation, err)
}

func websocketExpectedError(err error) bool {
	return websocketErrorTreeExpected(err)
}

func websocketErrorTreeExpected(err error) bool {
	if err == nil {
		return true
	}

	if unwrap, ok := err.(interface{ Unwrap() []error }); ok {
		causes := unwrap.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !websocketErrorTreeExpected(cause) {
				return false
			}
		}

		return true
	}
	if unwrap, ok := err.(interface{ Unwrap() error }); ok {
		if cause := unwrap.Unwrap(); cause != nil {
			return websocketErrorTreeExpected(cause)
		}
	}

	return websocketExpectedLeaf(err)
}

func websocketExpectedLeaf(err error) bool {
	switch err {
	case ErrClientDisconnected, context.Canceled, io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, websocket.ErrCloseSent, syscall.ECONNRESET, syscall.EPIPE:
		return true
	}

	var closeErr *websocket.CloseError

	return errors.As(err, &closeErr)
}
