// internal/observability/http.go; adapted: core's routeName() hard-coded
// the one route core had (create_run/get_run/get_result/health/ready/
// metrics). This platform layer has no public API routes of its own yet —
// auth/content/training/assessment/reporting each add their own later —
// so InstrumentHTTP takes a RouteNamer instead: the caller decides how a
// path maps to a low-cardinality route label (e.g. a chi route pattern).
// admin.go supplies AdminRouteNamer for its own three routes. The response
// recorder (Flusher/Hijacker/Pusher/ReaderFrom capability preservation,
// needed for SSE) is unchanged.
//
// RouteNamer runs before the handler. A nested ServeMux may clone the request
// before setting Request.Pattern, so reading Pattern afterward loses the
// inner route and collapses metrics to "unknown". PatternRouteNamer asks the
// owning mux which registered pattern will match instead.
package observability

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RouteNamer maps a request to a low-cardinality route label for metrics
// and logs — never the raw path, which could contain IDs.
type RouteNamer func(r *http.Request) string

// HTTPOption adds an optional sink to InstrumentHTTP.
type HTTPOption func(*httpOptions)

type httpOptions struct{ window *Window }

// WithWindow also feeds every finished request to window (ADR-038).
func WithWindow(window *Window) HTTPOption {
	return func(o *httpOptions) { o.window = window }
}

func InstrumentHTTP(next http.Handler, metrics *Metrics, logger Logger, routeName RouteNamer, options ...HTTPOption) http.Handler {
	var opts httpOptions
	for _, apply := range options {
		apply(&opts)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		route := routeName(r)
		writer, recorder := newResponseRecorder(w)
		next.ServeHTTP(writer, r)
		method := r.Method
		if !validMethod(method) {
			method = "OTHER"
		}
		class := strconv.Itoa(recorder.status/100) + "xx"
		elapsed := time.Since(started)
		metrics.ObserveHTTP(route, method, class, elapsed.Seconds())
		opts.window.Observe(route, method, recorder.status, elapsed)
		if !isProbeRoute(route) || recorder.status >= http.StatusInternalServerError {
			logger.Operation(r.Context(), 0, "http_request", class, recorder.Header().Get("X-Request-ID"), "")
		}
	})
}

// AdminRouteNamer names the three routes admin.go serves.
func AdminRouteNamer(r *http.Request) string {
	switch r.URL.Path {
	case "/healthz":
		return "health"
	case "/readyz":
		return "ready"
	case "/metrics":
		return "metrics"
	default:
		return "unknown"
	}
}

// PatternRouteNamer returns a RouteNamer backed by mux's registered patterns.
// Labels contain only the pattern, never concrete path IDs.
func PatternRouteNamer(mux *http.ServeMux) RouteNamer {
	return func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return routeLabel(pattern)
	}
}

func routeLabel(pattern string) string {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	pattern = strings.Trim(pattern, "/")
	if pattern == "" {
		return "unknown"
	}
	var label strings.Builder
	lastUnderscore := false
	for _, character := range strings.ToLower(pattern) {
		isLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		if isLetter || isDigit {
			label.WriteRune(character)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && label.Len() > 0 {
			label.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(label.String(), "_")
	if !validRoute(result) {
		return "unknown"
	}
	return result
}

func isProbeRoute(route string) bool {
	return route == "health" || route == "ready" || route == "metrics"
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseRecorder) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseRecorder) readFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(w.ResponseWriter, reader)
}

type flushRecorder struct {
	*responseRecorder
	flusher http.Flusher
}

func (w flushRecorder) Flush() { w.WriteHeader(http.StatusOK); w.flusher.Flush() }

type hijackRecorder struct {
	*responseRecorder
	hijacker http.Hijacker
}

func (w hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijacker.Hijack() }

type pushRecorder struct {
	*responseRecorder
	pusher http.Pusher
}

func (w pushRecorder) Push(target string, options *http.PushOptions) error {
	return w.pusher.Push(target, options)
}

type readFromRecorder struct {
	*responseRecorder
	readerFrom io.ReaderFrom
}

func (w readFromRecorder) ReadFrom(reader io.Reader) (int64, error) { return w.readFrom(reader) }

type flushHijackRecorder struct {
	flushRecorder
	hijacker http.Hijacker
}

func (w flushHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.hijacker.Hijack()
}

type flushPushRecorder struct {
	flushRecorder
	pusher http.Pusher
}

func (w flushPushRecorder) Push(target string, options *http.PushOptions) error {
	return w.pusher.Push(target, options)
}

type flushReadFromRecorder struct {
	flushRecorder
	readerFrom io.ReaderFrom
}

func (w flushReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) { return w.readFrom(reader) }

type hijackPushRecorder struct {
	hijackRecorder
	pusher http.Pusher
}

func (w hijackPushRecorder) Push(target string, options *http.PushOptions) error {
	return w.pusher.Push(target, options)
}

type hijackReadFromRecorder struct {
	hijackRecorder
	readerFrom io.ReaderFrom
}

func (w hijackReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) { return w.readFrom(reader) }

type pushReadFromRecorder struct {
	pushRecorder
	readerFrom io.ReaderFrom
}

func (w pushReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) { return w.readFrom(reader) }

type flushHijackPushRecorder struct {
	flushHijackRecorder
	pusher http.Pusher
}

func (w flushHijackPushRecorder) Push(target string, options *http.PushOptions) error {
	return w.pusher.Push(target, options)
}

type flushHijackReadFromRecorder struct {
	flushHijackRecorder
	readerFrom io.ReaderFrom
}

func (w flushHijackReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) {
	return w.readFrom(reader)
}

type flushPushReadFromRecorder struct {
	flushPushRecorder
	readerFrom io.ReaderFrom
}

func (w flushPushReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) {
	return w.readFrom(reader)
}

type hijackPushReadFromRecorder struct {
	hijackPushRecorder
	readerFrom io.ReaderFrom
}

func (w hijackPushReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) {
	return w.readFrom(reader)
}

type flushHijackPushReadFromRecorder struct {
	flushHijackPushRecorder
	readerFrom io.ReaderFrom
}

func (w flushHijackPushReadFromRecorder) ReadFrom(reader io.Reader) (int64, error) {
	return w.readFrom(reader)
}

func newResponseRecorder(response http.ResponseWriter) (http.ResponseWriter, *responseRecorder) {
	recorder := &responseRecorder{ResponseWriter: response, status: http.StatusOK}
	flusher, flushes := response.(http.Flusher)
	hijacker, hijacks := response.(http.Hijacker)
	pusher, pushes := response.(http.Pusher)
	readerFrom, readsFrom := response.(io.ReaderFrom)
	switch {
	case flushes && hijacks && pushes && readsFrom:
		return flushHijackPushReadFromRecorder{flushHijackPushRecorder{flushHijackRecorder{flushRecorder{recorder, flusher}, hijacker}, pusher}, readerFrom}, recorder
	case flushes && hijacks && pushes:
		return flushHijackPushRecorder{flushHijackRecorder{flushRecorder{recorder, flusher}, hijacker}, pusher}, recorder
	case flushes && hijacks && readsFrom:
		return flushHijackReadFromRecorder{flushHijackRecorder{flushRecorder{recorder, flusher}, hijacker}, readerFrom}, recorder
	case flushes && pushes && readsFrom:
		return flushPushReadFromRecorder{flushPushRecorder{flushRecorder{recorder, flusher}, pusher}, readerFrom}, recorder
	case hijacks && pushes && readsFrom:
		return hijackPushReadFromRecorder{hijackPushRecorder{hijackRecorder{recorder, hijacker}, pusher}, readerFrom}, recorder
	case flushes && hijacks:
		return flushHijackRecorder{flushRecorder{recorder, flusher}, hijacker}, recorder
	case flushes && pushes:
		return flushPushRecorder{flushRecorder{recorder, flusher}, pusher}, recorder
	case flushes && readsFrom:
		return flushReadFromRecorder{flushRecorder{recorder, flusher}, readerFrom}, recorder
	case hijacks && pushes:
		return hijackPushRecorder{hijackRecorder{recorder, hijacker}, pusher}, recorder
	case hijacks && readsFrom:
		return hijackReadFromRecorder{hijackRecorder{recorder, hijacker}, readerFrom}, recorder
	case pushes && readsFrom:
		return pushReadFromRecorder{pushRecorder{recorder, pusher}, readerFrom}, recorder
	case flushes:
		return flushRecorder{recorder, flusher}, recorder
	case hijacks:
		return hijackRecorder{recorder, hijacker}, recorder
	case pushes:
		return pushRecorder{recorder, pusher}, recorder
	case readsFrom:
		return readFromRecorder{recorder, readerFrom}, recorder
	default:
		return recorder, recorder
	}
}
