package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCountersAndExposition(t *testing.T) {
	r := NewRegistry()
	r.Inc("http_requests_total", "reqs", "group", "api", "status", "2xx")
	r.Inc("http_requests_total", "reqs", "group", "api", "status", "2xx")
	r.Inc("http_requests_total", "reqs", "group", "api", "status", "5xx")

	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()

	for _, want := range []string{
		`http_requests_total{group="api",status="2xx"} 2`,
		`http_requests_total{group="api",status="5xx"} 1`,
		"# TYPE http_requests_total counter",
		"process_uptime_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}

func TestMiddlewareCountsStatusClass(t *testing.T) {
	reg := NewRegistry()
	h := reg.Middleware("test")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

	w := httptest.NewRecorder()
	reg.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), `http_requests_total{group="test",method="GET",status="4xx"} 1`) {
		t.Fatalf("middleware did not count request:\n%s", w.Body.String())
	}
}
