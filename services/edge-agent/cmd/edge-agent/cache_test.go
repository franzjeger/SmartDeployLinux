package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func testCache(t *testing.T, handler http.HandlerFunc) (*blobCache, *httptest.Server) {
	t.Helper()
	origin := httptest.NewServer(handler)
	t.Cleanup(origin.Close)
	c := newBlobCache(t.TempDir(), origin.URL, 1<<30)
	return c, origin
}

func TestCache_FillOncePerObject(t *testing.T) {
	var fetches atomic.Int64
	c, _ := testCache(t, func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		_, _ = w.Write([]byte("kernel-bytes"))
	})

	// 20 concurrent clients, one upstream fetch.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, httptest.NewRequest("GET", "/static/linux-24.04/vmlinuz", nil))
			if rec.Code != 200 || rec.Body.String() != "kernel-bytes" {
				t.Errorf("resp %d %q", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	if got := fetches.Load(); got != 1 {
		t.Fatalf("origin fetched %d times, want 1", got)
	}
}

func TestCache_ServesRangeRequests(t *testing.T) {
	c, _ := testCache(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	})
	// Prime.
	c.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/static/a/b", nil))

	req := httptest.NewRequest("GET", "/static/a/b", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("range: %d %q", rec.Code, rec.Body.String())
	}
}

func TestCache_VerifiesBlobSHA(t *testing.T) {
	good := []byte("driver-pack-payload")
	sum := sha256.Sum256(good)
	sha := hex.EncodeToString(sum[:])

	c, _ := testCache(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blobs/"+sha[:2]+"/"+sha+"-pack.zip" {
			_, _ = w.Write(good)
			return
		}
		// Every other blob returns corrupted bytes.
		_, _ = w.Write([]byte("corrupted"))
	})

	// Correct content passes verification.
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/blobs/"+sha[:2]+"/"+sha+"-pack.zip", nil))
	if rec.Code != 200 {
		t.Fatalf("good blob: %d %s", rec.Code, rec.Body.String())
	}

	// A blob whose bytes don't match its content-addressed key is
	// refused — never cached, never served.
	otherSum := sha256.Sum256([]byte("something-else"))
	otherSha := hex.EncodeToString(otherSum[:])
	rec = httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/blobs/"+otherSha[:2]+"/"+otherSha+"-evil.zip", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("tampered blob served: %d", rec.Code)
	}
}

func TestCache_RejectsPathEscape(t *testing.T) {
	c, _ := testCache(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	})
	for _, p := range []string{"/static/../../etc/passwd", "/blobs/..%2f..%2fsecret"} {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code == 200 {
			t.Fatalf("path escape served: %s", p)
		}
	}
}

func TestCache_EvictsOldestUnderPressure(t *testing.T) {
	c, _ := testCache(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(make([]byte, 100))
	})
	c.maxBytes = 250 // fits two 100-byte objects, not three

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/static/obj-%d", i), nil))
		if rec.Code != 200 {
			t.Fatalf("obj-%d: %d", i, rec.Code)
		}
	}
	// obj-0 (oldest) should have been evicted to make room for obj-2.
	if _, ok := c.cachePath("/static/obj-0"); !ok {
		t.Fatal("cachePath failed")
	}
	var present int
	for i := 0; i < 3; i++ {
		p, _ := c.cachePath(fmt.Sprintf("/static/obj-%d", i))
		if fileExists(p) {
			present++
		}
	}
	if present > 2 {
		t.Fatalf("%d objects cached, cap allows 2", present)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
