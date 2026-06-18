package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrClientDisconnected is returned when the client has disconnected.
var ErrClientDisconnected = errors.New("sse: client disconnected")

type (
	// SseEvent represents a Server-Sent Event with optional fields.
	// According to the SSE spec, only Data is required.
	SseEvent struct {
		// Event specifies the event type (mapped to "event:" field).
		// If empty, the client will trigger the default "message" event.
		Event string
		// Data is the event payload (mapped to "data:" field).
		// Multi-line data is automatically handled by prefixing each line with "data:".
		Data string
		// Id specifies the event ID (mapped to "id:" field).
		// The browser's EventSource API uses this for reconnection via the Last-Event-ID header.
		Id string
		// Retry specifies the reconnection time in milliseconds (mapped to "retry:" field).
		// If 0, the field is omitted.
		Retry int
	}

	// SseResponseWriter is the interface required for SSE streaming.
	SseResponseWriter interface {
		http.ResponseWriter
		http.Flusher
	}

	// SseWriter provides methods to send Server-Sent Events to a client.
	SseWriter struct {
		ctx    context.Context
		cancel context.CancelFunc
		writer SseResponseWriter
		mu     sync.Mutex
	}
)

// DefaultSseHeartbeatInterval is the interval used for SSE heartbeat comments.
const DefaultSseHeartbeatInterval = 5 * time.Second

// NewSseWriter creates a new SSE writer that sends events to the provided response writer.
// It sets the necessary SSE headers (Content-Type, Cache-Control, Connection).
//
// The context is used to detect client disconnects. When the context is cancelled,
// subsequent Send/SendEvent calls will return ErrClientDisconnected.
//
// Note: This function does NOT set CORS headers. Configure CORS via middleware if needed.
//
// IMPORTANT: SSE responses should NOT be gzip-compressed, as compression buffers data
// and defeats real-time streaming. The gin-contrib/gzip middleware automatically skips
// compression when the client sends "Accept: text/event-stream" (which browser EventSource
// does by default). For non-browser clients, ensure they send this Accept header, or
// configure compression exclusions for your SSE endpoints.
func NewSseWriter(ctx context.Context, writer SseResponseWriter) *SseWriter {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	// Warn if gzip is active (should not happen if client sent Accept: text/event-stream)
	// We delete the Content-Encoding header as a safety measure, though ideally the gzip
	// middleware should not have activated in the first place.
	if writer.Header().Get("Content-Encoding") == "gzip" {
		writer.Header().Del("Content-Encoding")
		writer.Header().Del("Vary")
		// Note: The underlying gzip writer may still be active. This is a best-effort fix.
		// Properly exclude SSE paths from compression via middleware configuration.
	}

	writerCtx, cancel := context.WithCancel(ctx)
	sseWriter := &SseWriter{
		ctx:    writerCtx,
		cancel: cancel,
		writer: writer,
	}

	go sseWriter.heartbeatLoop()

	return sseWriter
}

// Send writes a simple data-only SSE event.
// This is a convenience method equivalent to SendEvent(SseEvent{Data: data}).
func (w *SseWriter) Send(data string) error {
	return w.SendEvent(SseEvent{Data: data})
}

// SendEvent writes a full SSE event with optional fields.
//
// The event is formatted according to the SSE specification:
// - event: <Event>    (omitted if Event is empty)
// - id: <Id>          (omitted if Id is empty)
// - retry: <Retry>    (omitted if Retry is 0)
// - data: <Data>      (multi-line data is split and each line prefixed with "data:")
//
// Returns ErrClientDisconnected if the client has disconnected.
func (w *SseWriter) SendEvent(event SseEvent) error {
	// Build the SSE event according to spec
	var buf bytes.Buffer

	if event.Event != "" {
		fmt.Fprintf(&buf, "event: %s\n", event.Event)
	}
	if event.Id != "" {
		fmt.Fprintf(&buf, "id: %s\n", event.Id)
	}
	if event.Retry > 0 {
		fmt.Fprintf(&buf, "retry: %d\n", event.Retry)
	}

	// Handle multi-line data: each line gets its own "data:" prefix
	if event.Data != "" {
		lines := strings.Split(event.Data, "\n")
		for _, line := range lines {
			fmt.Fprintf(&buf, "data: %s\n", line)
		}
	} else {
		// Even if data is empty, we need at least one "data:" line
		buf.WriteString("data: \n")
	}

	// Empty line terminates the event
	buf.WriteByte('\n')

	return w.write(buf.Bytes())
}

// Close stops any background heartbeats and releases resources.
func (w *SseWriter) Close() {
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *SseWriter) heartbeatLoop() {
	ticker := time.NewTicker(DefaultSseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if err := w.write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
		}
	}
}

func (w *SseWriter) write(payload []byte) error {
	if err := w.ctx.Err(); err != nil {
		return ErrClientDisconnected
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Reset write deadline to prevent timeout on long-lived connections.
	// Go's http.Server resets the write deadline on each Write(), but for idle SSE
	// connections (no writes for >WriteTimeout), the connection would be killed.
	// We reset it before each event to ensure the connection stays alive.
	rc := http.NewResponseController(w.writer)
	// Set deadline to "never" by using a far-future time.
	// Alternatively, set it to time.Now().Add(<some duration>) for a per-event timeout.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	if _, err := w.writer.Write(payload); err != nil {
		return err
	}
	w.writer.Flush()

	return nil
}
