// internal/observability/observability_test.go; adapted: namespace
// orchestration_core -> emsim, dot-namespaced test kinds instead of
// dialogue/judge, Logger.Operation drops the safeStage argument, and the
// sampler test drops Runs (see sampler.go/metrics.go).
package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsUseInjectedRegistryAndFixedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMetrics(registry, "api"); err == nil {
		t.Fatal("duplicate collectors accepted")
	}
	metrics.ObserveHTTP("get_run", "GET", "2xx", 0.01)
	metrics.ObserveHTTP("secret-transcript", "GET", "2xx", 0.01)
	metrics.SetQueueTasks("scenario.generate", "pending", 2)
	metrics.SetQueueTasks("run-123", "payload", 2)
	text := exposition(t, registry)
	for _, want := range []string{"emsim_http_requests_total", `route="get_run"`, `task_kind="scenario.generate"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	for _, forbidden := range []string{"secret-transcript", "run-123", "payload"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("forbidden label %q in %s", forbidden, text)
		}
	}
}

func TestLoggerExcludesPayloadAndExternalErrors(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(slog.New(slog.NewJSONHandler(&output, nil)), "api", "api")
	logger.Operation(context.Background(), slog.LevelError, "http_request", "failed", "request-1", "database_unavailable")
	logger.Operation(context.Background(), slog.LevelError, "provider response", "synthetic-transcript", "", "postgres://synthetic-secret")
	text := output.String()
	if !strings.Contains(text, "request-1") || !strings.Contains(text, "database_unavailable") {
		t.Fatalf("safe fields missing: %s", text)
	}
	for _, forbidden := range []string{"transcript", "postgres://", "bearer ", "provider response"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe log field %q in %s", forbidden, text)
		}
	}
}

func TestSamplerResetsDisappearedCategoriesAndSurvivesFailure(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "maintenance")
	if err != nil {
		t.Fatal(err)
	}
	ticks := &fakeTicker{ch: make(chan time.Time, 1)}
	reader := &snapshotReader{snapshots: []Snapshot{{Tasks: []TaskCount{{Kind: "scenario.generate", Status: "pending", Count: 3}}}, {}}, called: make(chan struct{}, 3)}
	sampler, err := NewSampler(reader, metrics, NewLogger(nil, "worker", "maintenance"), time.Second, time.Second, fakeTickerFactory{ticker: ticks})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sampler.Run(ctx) }()
	ticks.ch <- time.Now()
	<-reader.called
	if got := exposition(t, registry); !strings.Contains(got, `emsim_queue_tasks status="pending" task_kind="scenario.generate" 3`) {
		t.Fatalf("initial sample missing: %s", got)
	}
	ticks.ch <- time.Now()
	<-reader.called
	if !strings.Contains(exposition(t, registry), `emsim_queue_tasks status="pending" task_kind="scenario.generate" 0`) {
		t.Fatal("disappeared task was not reset")
	}
	reader.err = errors.New("database unavailable: synthetic-secret")
	ticks.ch <- time.Now()
	<-reader.called
	if !strings.Contains(exposition(t, registry), `emsim_sampler_samples_total outcome="failed" 1`) {
		t.Fatal("failed sample missing")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !ticks.stopped {
		t.Fatal("ticker not stopped")
	}
}

func TestResponseRecorderPreservesCapabilitiesAndFirstStatus(t *testing.T) {
	response := &recordingWriter{header: make(http.Header)}
	writer, recorder := newResponseRecorder(response)
	writer.WriteHeader(201)
	writer.WriteHeader(503)
	if recorder.status != 201 || response.status != 201 || recorder.Unwrap() != http.ResponseWriter(response) {
		t.Fatalf("recorded status = %d/%d", recorder.status, response.status)
	}
	if _, ok := writer.(http.Flusher); ok {
		t.Fatal("added Flusher")
	}
	if _, ok := writer.(io.ReaderFrom); ok {
		t.Fatal("added ReaderFrom")
	}
	flushResponse := &flushWriter{recordingWriter: recordingWriter{header: make(http.Header)}}
	writer, _ = newResponseRecorder(flushResponse)
	if _, ok := writer.(http.Flusher); !ok {
		t.Fatal("missing Flusher")
	}
	writer.(http.Flusher).Flush()
	if flushResponse.status != http.StatusOK || !flushResponse.flushed {
		t.Fatal("flush did not commit status")
	}
	readerResponse := &readFromWriter{recordingWriter: recordingWriter{header: make(http.Header)}}
	writer, _ = newResponseRecorder(readerResponse)
	readerFrom, ok := writer.(io.ReaderFrom)
	if !ok {
		t.Fatal("missing ReaderFrom")
	}
	if _, err := readerFrom.ReadFrom(strings.NewReader("body")); err != nil || readerResponse.body.String() != "body" {
		t.Fatalf("ReadFrom error/body = %v/%q", err, readerResponse.body.String())
	}
}

func exposition(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for _, metric := range metrics {
		output.WriteString(metric.GetName())
		for _, sample := range metric.GetMetric() {
			for _, label := range sample.GetLabel() {
				output.WriteString(" " + label.GetName() + "=\"" + label.GetValue() + "\"")
			}
			if sample.Gauge != nil {
				output.WriteString(" " + formatGauge(sample.GetGauge().GetValue()))
			}
			if sample.Counter != nil {
				output.WriteString(" " + formatGauge(sample.GetCounter().GetValue()))
			}
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func formatGauge(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

type fakeTicker struct {
	ch      chan time.Time
	stopped bool
}
type recordingWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *recordingWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

type flushWriter struct {
	recordingWriter
	flushed bool
}

func (w *flushWriter) Flush() { w.flushed = true }

type readFromWriter struct{ recordingWriter }

func (w *readFromWriter) ReadFrom(reader io.Reader) (int64, error) { return io.Copy(&w.body, reader) }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.stopped = true }

type fakeTickerFactory struct{ ticker *fakeTicker }

func (f fakeTickerFactory) NewTicker(time.Duration) Ticker { return f.ticker }

type snapshotReader struct {
	snapshots []Snapshot
	current   int
	err       error
	called    chan struct{}
}

func (r *snapshotReader) Sample(context.Context) (Snapshot, error) {
	r.called <- struct{}{}
	if r.err != nil {
		return Snapshot{}, r.err
	}
	result := r.snapshots[r.current]
	if r.current < len(r.snapshots)-1 {
		r.current++
	}
	return result, nil
}
