// Minimal, dependency-free Prometheus-format metrics.
//
// We deliberately avoid pulling in the full Prometheus client: the API
// needs a handful of counters and one histogram-ish summary, and the
// text exposition format is trivial to emit. If richer instrumentation
// is needed later, this package's call sites are the seam to swap at.

package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Registry struct {
	mu       sync.Mutex
	counters map[string]map[string]float64 // metric -> labelset -> value
	help     map[string]string
	started  time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]map[string]float64{},
		help:     map[string]string{},
		started:  time.Now(),
	}
}

// Inc increments a counter. labels must be provided as alternating
// key/value pairs and must be low-cardinality (method, status class —
// never raw paths or IDs).
func (r *Registry) Inc(name, help string, labels ...string) {
	r.Add(name, help, 1, labels...)
}

func (r *Registry) Add(name, help string, delta float64, labels ...string) {
	key := labelKey(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.counters[name]
	if !ok {
		m = map[string]float64{}
		r.counters[name] = m
		r.help[name] = help
	}
	m[key] += delta
}

func labelKey(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	out := "{"
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			out += ","
		}
		out += labels[i] + "=" + strconv.Quote(labels[i+1])
	}
	return out + "}"
}

// Handler serves the Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.mu.Lock()
		defer r.mu.Unlock()
		names := make([]string, 0, len(r.counters))
		for n := range r.counters {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", n, r.help[n], n)
			keys := make([]string, 0, len(r.counters[n]))
			for k := range r.counters[n] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "%s%s %g\n", n, k, r.counters[n][k])
			}
		}
		fmt.Fprintf(w, "# HELP process_uptime_seconds Seconds since process start\n# TYPE process_uptime_seconds gauge\nprocess_uptime_seconds %g\n",
			time.Since(r.started).Seconds())
	})
}

// Middleware counts requests by method and status class, and total
// request duration, keyed by route pattern group (the caller passes a
// static group name, not the raw path, to keep cardinality bounded).
func (r *Registry) Middleware(group string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, req)
			class := strconv.Itoa(sw.status/100) + "xx"
			r.Inc("http_requests_total", "HTTP requests by group/method/status class",
				"group", group, "method", req.Method, "status", class)
			r.Add("http_request_seconds_total", "Total request handling seconds by group",
				time.Since(start).Seconds(), "group", group)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
