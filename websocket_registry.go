package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/log"
)

type websocketRegistryKey struct{}

const websocketHttpUnavailableKey = "httpserver.websocket.httpUnavailable"

func markHttpUnavailable(ctx *gin.Context) {
	ctx.Set(websocketHttpUnavailableKey, true)
}

func httpUnavailable(ctx *gin.Context) bool {
	unavailable, _ := ctx.Get(websocketHttpUnavailableKey)

	return unavailable == true
}

func websocketRegistryFromContext(ctx context.Context) *websocketRegistry {
	registry, _ := ctx.Value(websocketRegistryKey{}).(*websocketRegistry)

	return registry
}

type websocketRegistry struct {
	options WebsocketOptions
	logger  log.Logger
	mu      sync.Mutex
	stopped bool
	entries map[*websocketReservation]struct{}
	done    chan struct{}
}

type websocketReservation struct {
	registry *websocketRegistry
	raw      net.Conn
	conn     *WebsocketConn
}

func newWebsocketRegistry(logger log.Logger, options WebsocketOptions) *websocketRegistry {
	done := make(chan struct{})
	close(done)

	return &websocketRegistry{
		logger:  logger,
		options: options,
		entries: make(map[*websocketReservation]struct{}),
		done:    done,
	}
}

func (r *websocketRegistry) reserve() (*websocketReservation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, false
	}
	if len(r.entries) == 0 {
		r.done = make(chan struct{})
	}
	entry := &websocketReservation{registry: r}
	r.entries[entry] = struct{}{}

	return entry, true
}

func (r *websocketRegistry) stopAdmission() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

func (e *websocketReservation) attachRaw(raw net.Conn) bool {
	r := e.registry
	r.mu.Lock()
	accepted := !r.stopped
	if accepted {
		e.raw = raw
	}
	r.mu.Unlock()
	if !accepted {
		// A late hijack is fenced even if it missed the shutdown snapshot.
		if err := raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			r.logger.Debug(context.Background(), "close late websocket socket: %w", err)
		}
	}

	return accepted
}

func (e *websocketReservation) promote(conn *WebsocketConn) bool {
	r := e.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	e.conn = conn

	return true
}

func (e *websocketReservation) release() {
	r := e.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, present := r.entries[e]; !present {
		return
	}
	delete(r.entries, e)
	if len(r.entries) == 0 {
		close(r.done)
	}
}

type websocketShutdownEntry struct {
	raw  net.Conn
	conn *WebsocketConn
}

func (r *websocketRegistry) shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.stopped = true
	entries := make([]websocketShutdownEntry, 0, len(r.entries))
	for entry := range r.entries {
		entries = append(entries, websocketShutdownEntry{raw: entry.raw, conn: entry.conn})
	}
	done := r.done
	r.mu.Unlock()

	deadline := time.Now().Add(r.options.CloseTimeout)
	if shutdownDeadline, ok := ctx.Deadline(); ok && shutdownDeadline.Before(deadline) {
		deadline = shutdownDeadline
	}

	// All peers receive the same absolute close deadline, not N sequential budgets.
	results := make(chan error, len(entries))
	for _, entry := range entries {
		go func() {
			results <- entry.close(deadline)
		}()
	}
	for range entries {
		if err := <-results; err != nil && !websocketExpectedError(err) {
			// Close frames are best effort; the socket has already been closed.
			// Completion below, not a peer's willingness to read, gates shutdown.
			r.logger.Debug(ctx, "terminate websocket during shutdown: %w", err)
		}
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// An empty/already-drained registry is successful even when the
		// configured shutdown budget has already expired.
		select {
		case <-done:
			return nil
		default:
			return fmt.Errorf("wait for websocket requests and callbacks: %w", ctx.Err())
		}
	}
}

func (e websocketShutdownEntry) close(deadline time.Time) error {
	if e.conn != nil {
		if err := e.conn.closeWithDeadline(1001, "server shutting down", deadline); err != nil {
			return fmt.Errorf("close established websocket: %w", err)
		}
	} else if e.raw != nil {
		if err := e.raw.Close(); err != nil {
			return fmt.Errorf("close pending websocket handshake: %w", err)
		}
	}

	return nil
}
