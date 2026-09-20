package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"emsim/internal/platform/observability"

	"github.com/jackc/pgx/v5/pgxpool"
)

// reconnectDelay is how long RunListener waits before retrying LISTEN
// after losing the connection — short enough that a dropped connection
// recovers quickly, long enough not to hammer PostgreSQL while it is
// unavailable.
const reconnectDelay = 2 * time.Second

// RunListener holds one dedicated PostgreSQL connection LISTENing on
// Channel and feeds every notification into hub, until ctx is
// cancelled. It never returns early on a connection failure — it logs
// (via the same whitelisted-shape Logger every process uses) and
// retries, calling hub.Reset() before each successful (re)subscription
// so every cursor issued under a lost session becomes unresolvable
// (ADR-018: resync on "восстановление LISTEN"). This is the api
// process's own long-lived background loop — cmd/emsim starts exactly
// one, before recovery/readiness (see api.go). ready, if non-nil, is
// closed exactly once, right after the very first successful LISTEN —
// the api process waits on it before running C6's restart-recovery step,
// so nothing that happens during recovery can be missed for want of a
// subscription that was not there yet.
func RunListener(ctx context.Context, pool *pgxpool.Pool, hub *Hub, logger observability.Logger, ready chan<- struct{}) {
	var once sync.Once
	markReady := func() {
		if ready == nil {
			return
		}
		once.Do(func() { close(ready) })
	}
	for ctx.Err() == nil {
		if err := listenOnce(ctx, pool, hub, markReady); err != nil && ctx.Err() == nil {
			logger.Operation(ctx, slog.LevelWarn, "realtime_listen", "reconnect", "", "listen_failed")
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// listenOnce holds one connection for as long as it stays healthy,
// returning only when the connection is lost or ctx is done.
// markReady is called after this call's own successful LISTEN (a no-op
// on every call after the first, via RunListener's sync.Once).
func listenOnce(ctx context.Context, pool *pgxpool.Pool, hub *Hub, markReady func()) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `LISTEN `+Channel); err != nil {
		return err
	}
	hub.Reset()
	markReady()

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		var payload wirePayload
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			continue // malformed payload from some future producer bug — drop, do not crash the listener
		}
		hub.Publish(Event{LessonID: payload.LessonID, UserID: payload.UserID, ItemID: payload.ItemID})
	}
}
