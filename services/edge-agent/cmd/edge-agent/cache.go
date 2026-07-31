// Site-local image mirror: a verifying, caching HTTP proxy for the
// deploy server's blob paths. This is what makes a 40-machine site cost
// one WAN image transfer instead of forty — the reliable equivalent of
// WDS multicast for the WAN leg, with plain unicast HTTP on the LAN leg
// (which modern switches handle fine and every client already speaks).
//
//   GET /static/<path>   — cached passthrough of origin /static/<path>
//   GET /blobs/<key>     — cached passthrough; keys embed a sha256
//                          (<2-hex>/<64-hex>-<name>) which is VERIFIED
//                          while filling the cache, so a corrupted or
//                          tampered WAN transfer never gets served
//   GET /healthz
//
// Enabled when EDGE_CACHE_LISTEN is set (e.g. ":8090"). Register the
// site in the deploy server (PUT /api/v1/sites {name, mirror_base_url})
// and the render pipeline rewrites machines' media URLs here.
//
// Design notes:
//   - Fill-once: concurrent requests for the same object share one
//     upstream download (in-flight map), so a simultaneous PXE storm
//     still costs a single WAN fetch.
//   - Range requests (wimboot, DISM resume) are served from the cached
//     file via http.ServeContent. A request arriving mid-fill waits for
//     the fill to complete rather than streaming a partial file.
//   - Eviction: before each fill, oldest-mtime files are deleted until
//     the new object fits under EDGE_CACHE_MAX_BYTES (default 50 GiB).

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var blobKeyShaRe = regexp.MustCompile(`^[0-9a-f]{2}/([0-9a-f]{64})-`)

type blobCache struct {
	dir      string
	origin   string // e.g. https://deploy.example.com
	maxBytes int64
	client   *http.Client

	mu       sync.Mutex
	inflight map[string]chan struct{} // cache-relative path -> done
}

func newBlobCache(dir, origin string, maxBytes int64) *blobCache {
	return &blobCache{
		dir: dir, origin: origin, maxBytes: maxBytes,
		client:   &http.Client{Timeout: 0}, // multi-GB fills; no client timeout
		inflight: map[string]chan struct{}{},
	}
}

// cachePath maps a URL path to its on-disk location, rejecting escapes.
func (c *blobCache) cachePath(urlPath string) (string, bool) {
	rel := strings.TrimPrefix(urlPath, "/")
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false
	}
	return filepath.Join(c.dir, clean), true
}

func (c *blobCache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/static/") && !strings.HasPrefix(r.URL.Path, "/blobs/") {
		http.NotFound(w, r)
		return
	}
	path, ok := c.cachePath(r.URL.Path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if err := c.ensure(r.Context(), r.URL.Path, path); err != nil {
		slog.Error("cache fill", "path", r.URL.Path, "err", err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "cache read", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "cache stat", http.StatusInternalServerError)
		return
	}
	// Touch mtime so eviction is LRU-ish.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	w.Header().Set("X-Deploy-Cache", "hit")
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// ensure guarantees the object exists in the cache, downloading (and
// verifying, for /blobs) at most once across concurrent callers.
func (c *blobCache) ensure(ctx context.Context, urlPath, path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		c.mu.Lock()
		if done, busy := c.inflight[path]; busy {
			c.mu.Unlock()
			select {
			case <-done:
				continue // filled (or failed); loop re-checks
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		done := make(chan struct{})
		c.inflight[path] = done
		c.mu.Unlock()

		err := c.fill(ctx, urlPath, path)
		c.mu.Lock()
		delete(c.inflight, path)
		close(done)
		c.mu.Unlock()
		return err
	}
}

func (c *blobCache) fill(ctx context.Context, urlPath, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+urlPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("origin %d", resp.StatusCode)
	}

	if resp.ContentLength > 0 {
		if err := c.evictFor(resp.ContentLength); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fill-*")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		return err
	}

	// Blob keys are content-addressed: verify before serving anyone.
	if m := blobKeyShaRe.FindStringSubmatch(strings.TrimPrefix(urlPath, "/blobs/")); m != nil {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != m[1] {
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, m[1])
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// evictFor deletes oldest files until `need` bytes fit under maxBytes.
func (c *blobCache) evictFor(need int64) error {
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	var entries []entry
	var total int64
	_ = filepath.Walk(c.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasPrefix(filepath.Base(p), ".fill-") {
			return nil
		}
		entries = append(entries, entry{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total+need <= c.maxBytes {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })
	for _, e := range entries {
		if total+need <= c.maxBytes {
			return nil
		}
		if err := os.Remove(e.path); err == nil {
			total -= e.size
			slog.Info("cache evicted", "path", e.path, "freed", e.size)
		}
	}
	if total+need > c.maxBytes {
		return fmt.Errorf("object (%d bytes) larger than cache limit (%d)", need, c.maxBytes)
	}
	return nil
}

// runBlobCache starts the mirror HTTP server. No-op unless
// EDGE_CACHE_LISTEN is configured.
func runBlobCache(ctx context.Context) {
	listen := os.Getenv("EDGE_CACHE_LISTEN")
	if listen == "" {
		slog.Info("EDGE_CACHE_LISTEN not set; site image mirror disabled")
		return
	}
	origin := os.Getenv("DEPLOY_URL")
	if origin == "" {
		origin = "https://" + os.Getenv("DEPLOY_FQDN")
	}
	dir := os.Getenv("EDGE_CACHE_DIR")
	if dir == "" {
		dir = "/var/cache/deploy-images"
	}
	maxBytes := int64(50) << 30
	if v := os.Getenv("EDGE_CACHE_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("cache dir", "dir", dir, "err", err)
		return
	}

	cache := newBlobCache(dir, origin, maxBytes)
	mux := http.NewServeMux()
	mux.Handle("/static/", cache)
	mux.Handle("/blobs/", cache)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("site image mirror listening", "addr", listen, "dir", dir, "origin", origin, "max_bytes", maxBytes)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("image mirror listen", "err", err)
	}
}
