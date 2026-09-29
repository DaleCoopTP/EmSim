package realtime

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// epochSeq is a process-wide monotonic counter, not wall-clock time: two
// epochs minted back to back (NewHub immediately followed by the
// Listener's first Reset, or two reconnects in the same tick) must
// never collide, and time.Now().UnixNano() is not guaranteed distinct
// at that resolution on every platform.
var epochSeq atomic.Uint64

func nextEpoch() uint64 { return epochSeq.Add(1) }

// bufferCap is ADR-018's fixed ring buffer size ("буфер 1000 сообщений").
const bufferCap = 1000

// Hub fans one PostgreSQL LISTEN session out to any number of readers.
// One process epoch covers one continuous LISTEN session: it changes
// only when the Listener (re)establishes LISTEN (process start, or a
// reconnect after the connection was lost), never on an ordinary
// Publish. A cursor from a different epoch is unconditionally stale —
// RFC-001 §7.7: "неизвестный cursor... восстановление LISTEN всегда
// отправляет resync". Hub itself never touches PostgreSQL; Listener
// does that and calls Publish/Reset.
type Hub struct {
	// streams counts open SSE connections (ADR-038's load panel).
	streams atomic.Int64

	mu      sync.Mutex
	epoch   uint64
	counter uint64
	oldest  uint64 // counter of buffer[0]; 0 means buffer is empty
	buffer  []Event
	waiters chan struct{}
}

// StreamOpened and StreamClosed bracket one SSE connection's lifetime.
func (h *Hub) StreamOpened() { h.streams.Add(1) }
func (h *Hub) StreamClosed() { h.streams.Add(-1) }

// Streams is how many SSE connections are open right now.
func (h *Hub) Streams() int { return int(h.streams.Load()) }

func NewHub() *Hub {
	return &Hub{epoch: nextEpoch(), waiters: make(chan struct{})}
}

// Reset starts a new epoch and clears the buffer — called by Listener
// each time it (re)establishes LISTEN, including the very first time.
// Every cursor issued under a previous epoch becomes unresolvable,
// which is exactly the resync trigger RFC-001 §7.7 requires for a
// restored LISTEN session (a gap it cannot otherwise prove is gap-free).
func (h *Hub) Reset() {
	h.mu.Lock()
	h.epoch = nextEpoch()
	h.counter = 0
	h.oldest = 0
	h.buffer = h.buffer[:0]
	old := h.waiters
	h.waiters = make(chan struct{})
	h.mu.Unlock()
	close(old)
}

// Publish appends one event, assigning it the next counter under the
// current epoch, and wakes every reader blocked in Wait.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	h.counter++
	e.Epoch, e.Counter = h.epoch, h.counter
	h.buffer = append(h.buffer, e)
	if len(h.buffer) > bufferCap {
		h.buffer = h.buffer[1:]
	}
	if len(h.buffer) > 0 {
		h.oldest = h.buffer[0].Counter
	}
	old := h.waiters
	h.waiters = make(chan struct{})
	h.mu.Unlock()
	close(old)
}

// Cursor returns the current head cursor — what a freshly connecting
// stream hands back as stream.ready before it has consumed anything.
func (h *Hub) Cursor() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return formatCursor(h.epoch, h.counter)
}

// Wait returns a channel that closes the next time Publish or Reset
// runs — select on it alongside a heartbeat ticker and the request
// context in an SSE loop.
func (h *Hub) Wait() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waiters
}

// Since resolves a client-supplied cursor (Last-Event-ID or the initial
// query param) against the current buffer. resync is true whenever the
// cursor cannot be trusted to replay a gap-free sequence: malformed,
// from a different (now-superseded) epoch, older than the oldest
// buffered event, or ahead of the current counter (should not happen,
// but is treated the same defensively). On success it returns every
// event strictly after cursor, oldest first.
func (h *Hub) Since(cursor string) (events []Event, newCursor string, resync bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	newCursor = formatCursor(h.epoch, h.counter)
	epoch, counter, ok := parseCursor(cursor)
	if !ok || epoch != h.epoch || counter > h.counter {
		return nil, newCursor, true
	}
	if h.oldest > 0 && counter < h.oldest-1 {
		// The client's last-seen counter falls strictly before the
		// buffer's own coverage — something in between was evicted.
		return nil, newCursor, true
	}
	if counter == h.counter {
		return nil, newCursor, false
	}
	out := make([]Event, 0, len(h.buffer))
	for _, e := range h.buffer {
		if e.Counter > counter {
			out = append(out, e)
		}
	}
	return out, newCursor, false
}

func formatCursor(epoch, counter uint64) string {
	return fmt.Sprintf("%d:%d", epoch, counter)
}

func parseCursor(cursor string) (epoch, counter uint64, ok bool) {
	parts := strings.SplitN(cursor, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	e, err1 := strconv.ParseUint(parts[0], 10, 64)
	c, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return e, c, true
}
