// New test: slice-4-plan.md's C11 — the SSE half of RFC-001 §7.7 driven
// over real HTTP against a live "emsim api" process, which nothing else
// in this repository exercises (internal/platform/realtime's own tests
// cover Hub.Since/Reset directly; realtime_test.go covers LISTEN/NOTIFY
// into a Hub; neither drives GET /lessons/{id}/stream or GET /my/stream
// themselves). Covers: stream-first snapshot ordering (stream.ready
// before anything else), Last-Event-ID reconnect replay, the ?cursor
// query fallback a brand-new EventSource must use instead, resync on an
// unknown/stale cursor, and per-subscriber filtering (an instructor's
// lesson stream and a trainee's own stream never leak another user's
// events).
//
//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseFrame is one parsed "event: ...\nid: ...\ndata: ...\n\n" block.
type sseFrame struct {
	event string
	id    string
	data  map[string]string
}

// sseClient reads an open text/event-stream response line by line on its
// own goroutine so the test can wait for the next frame with a timeout
// instead of blocking forever on a connection that never sends one.
type sseClient struct {
	lines  chan string
	cancel context.CancelFunc
	body   interface{ Close() error }
}

// openSSE opens path as an SSE connection using client's own session
// (its cookie jar must already be authenticated). lastEventID, when
// non-empty, is sent as the Last-Event-ID header — the reconnect path a
// live EventSource uses. client must not carry a request timeout (a
// timed-out http.Client kills a streaming body after its Timeout
// elapses even mid-stream), unlike the short-lived clients the rest of
// this package's e2e tests use for plain JSON requests.
func openSSE(t *testing.T, ctx context.Context, client *http.Client, baseURL, path, lastEventID string) *sseClient {
	t.Helper()
	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET %s status = %d", path, resp.StatusCode)
	}
	c := &sseClient{lines: make(chan string, 64), cancel: cancel, body: resp.Body}
	go func() {
		defer close(c.lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			c.lines <- scanner.Text()
		}
	}()
	t.Cleanup(c.close)
	return c
}

func (c *sseClient) close() {
	c.cancel()
	_ = c.body.Close()
}

// next reads lines until one full frame (a blank line terminates it, per
// the SSE wire format) arrives, or timeout elapses first. A heartbeat
// comment (": heartbeat") carries no "event:" line and is skipped rather
// than returned as an empty frame.
func (c *sseClient) next(t *testing.T, timeout time.Duration) (sseFrame, bool) {
	t.Helper()
	var frame sseFrame
	haveEvent := false
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return frame, false
			}
			switch {
			case line == "":
				if haveEvent {
					return frame, true
				}
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
				haveEvent = true
			case strings.HasPrefix(line, "id: "):
				frame.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				var data map[string]string
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
				frame.data = data
			}
		case <-deadline:
			return frame, false
		}
	}
}

// sseHTTPClient shares base's cookie jar (so the SSE connection carries
// the same authenticated session) but, unlike base itself, has no
// request Timeout — a streaming GET must be allowed to stay open.
func sseHTTPClient(base *http.Client) *http.Client {
	return &http.Client{Jar: base.Jar}
}

// TestAPIProcessSSEStreamSnapshotReplayResyncAndFiltering is slice-4-
// plan.md's C11: a real instructor lesson stream and two trainees' own
// streams, driven purely over HTTP against a live "emsim api" process.
func TestAPIProcessSSEStreamSnapshotReplayResyncAndFiltering(t *testing.T) {
	f := setupTrainingE2E(t)
	adminClient := newCookieClient(t)
	loginAdmin(t, f, adminClient)

	if response := jsonRequest(t, f.ctx, adminClient, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "e2e-sse-instructor", "password": "correct-horse-battery-staple",
		"full_name": "Инструктор SSE", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor status = %d", response.StatusCode)
	}
	instructorClient := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": "e2e-sse-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login status = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructorClient, "pilot-tree-01")

	provisionWorkstations(t, f, adminClient,
		map[string]any{"number": 1, "label": "e2e-sse-a"},
		map[string]any{"number": 2, "label": "e2e-sse-b"},
	)
	traineeAClient := createTrainee(t, f, adminClient, "e2e-sse-a", 1)
	traineeBClient := createTrainee(t, f, adminClient, "e2e-sse-b", 2)
	traineeAID := currentUserID(t, f, traineeAClient)
	_ = currentUserID(t, f, traineeBClient)

	lesson := runLesson(t, f, instructorClient, "SSE занятие", "training", 1, traineeAID, versionID)

	instructorSSE := sseHTTPClient(instructorClient)
	traineeASSE := sseHTTPClient(traineeAClient)
	traineeBSSE := sseHTTPClient(traineeBClient)

	// -------------------------------------------------- stream-first snapshot ordering
	instructorStream := openSSE(t, f.ctx, instructorSSE, f.baseURL, "/api/v1/lessons/"+lesson.ID+"/stream", "")
	ready, ok := instructorStream.next(t, 5*time.Second)
	if !ok || ready.event != "stream.ready" || ready.id == "" {
		t.Fatalf("instructor stream first frame = %+v, ok=%v, want stream.ready with a cursor", ready, ok)
	}

	traineeAStream := openSSE(t, f.ctx, traineeASSE, f.baseURL, "/api/v1/my/stream", "")
	if readyA, ok := traineeAStream.next(t, 5*time.Second); !ok || readyA.event != "stream.ready" {
		t.Fatalf("trainee A stream first frame = %+v, ok=%v, want stream.ready", readyA, ok)
	}
	traineeBStream := openSSE(t, f.ctx, traineeBSSE, f.baseURL, "/api/v1/my/stream", "")
	if readyB, ok := traineeBStream.next(t, 5*time.Second); !ok || readyB.event != "stream.ready" {
		t.Fatalf("trainee B stream first frame = %+v, ok=%v, want stream.ready", readyB, ok)
	}

	// -------------------------------------------------- a domain change reaches only the matching subscribers
	itemID := currentItemID(t, f, traineeAClient)
	if resp, receipt := sendCommand(t, f, traineeAClient, itemID, randomCommandID(), 0, "open", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open status=%d receipt=%+v", resp.StatusCode, receipt)
	}

	openEvent, ok := instructorStream.next(t, 5*time.Second)
	if !ok || openEvent.event != "invalidate" || openEvent.data["lesson_id"] != lesson.ID || openEvent.data["item_id"] != itemID {
		t.Fatalf("instructor stream after open = %+v, ok=%v, want an invalidate for lesson=%s item=%s", openEvent, ok, lesson.ID, itemID)
	}
	lastEventID := openEvent.id

	openEventA, ok := traineeAStream.next(t, 5*time.Second)
	if !ok || openEventA.event != "invalidate" || openEventA.data["user_id"] != traineeAID || openEventA.data["item_id"] != itemID {
		t.Fatalf("trainee A stream after open = %+v, ok=%v, want an invalidate for user=%s item=%s", openEventA, ok, traineeAID, itemID)
	}

	// Trainee B has no run at all in this lesson — their own /my/stream
	// (filtered by user id, not lesson id) must not see A's action.
	if frame, ok := traineeBStream.next(t, 1500*time.Millisecond); ok {
		t.Fatalf("trainee B stream received %+v, want nothing (filtering must not leak another user's events)", frame)
	}

	// -------------------------------------------------- reconnect via Last-Event-ID replays what was missed while disconnected
	instructorStream.close()
	if resp, receipt := sendCommand(t, f, traineeAClient, itemID, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	// Drain trainee A's own stream so it does not carry this event over
	// into the next assertion by accident.
	if _, ok := traineeAStream.next(t, 5*time.Second); !ok {
		t.Fatal("trainee A stream did not see the accept invalidate")
	}

	reconnected := openSSE(t, f.ctx, instructorSSE, f.baseURL, "/api/v1/lessons/"+lesson.ID+"/stream", lastEventID)
	readyAfterReconnect, ok := reconnected.next(t, 5*time.Second)
	if !ok || readyAfterReconnect.event != "stream.ready" {
		t.Fatalf("reconnected stream first frame = %+v, ok=%v, want stream.ready", readyAfterReconnect, ok)
	}
	replay, ok := reconnected.next(t, 5*time.Second)
	if !ok || replay.event != "invalidate" || replay.data["item_id"] != itemID {
		t.Fatalf("reconnected stream replay = %+v, ok=%v, want the accept invalidate replayed via Last-Event-ID", replay, ok)
	}
	reconnected.close()

	// -------------------------------------------------- the ?cursor query fallback (a brand-new EventSource cannot set headers)
	byQuery := openSSE(t, f.ctx, instructorSSE, f.baseURL, "/api/v1/lessons/"+lesson.ID+"/stream?cursor="+lastEventID, "")
	if readyQ, ok := byQuery.next(t, 5*time.Second); !ok || readyQ.event != "stream.ready" {
		t.Fatalf("cursor-query stream first frame = %+v, ok=%v, want stream.ready", readyQ, ok)
	}
	replayQ, ok := byQuery.next(t, 5*time.Second)
	if !ok || replayQ.event != "invalidate" || replayQ.data["item_id"] != itemID {
		t.Fatalf("cursor-query stream replay = %+v, ok=%v, want the same accept invalidate replayed", replayQ, ok)
	}
	byQuery.close()

	// -------------------------------------------------- an unresolvable cursor forces resync, never a silent gap
	stale := openSSE(t, f.ctx, instructorSSE, f.baseURL, "/api/v1/lessons/"+lesson.ID+"/stream", "999999999:1")
	if readyStale, ok := stale.next(t, 5*time.Second); !ok || readyStale.event != "stream.ready" {
		t.Fatalf("stale-cursor stream first frame = %+v, ok=%v, want stream.ready", readyStale, ok)
	}
	resync, ok := stale.next(t, 5*time.Second)
	if !ok || resync.event != "resync" {
		t.Fatalf("stale-cursor stream second frame = %+v, ok=%v, want resync", resync, ok)
	}
	stale.close()
}
