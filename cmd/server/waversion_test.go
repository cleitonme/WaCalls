package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store"
)

func testWAVersionResolver(urls ...string) *waVersionResolver {
	r := newWAVersionResolver(slog.New(slog.NewTextHandler(io.Discard, nil)), urls)
	r.client = &http.Client{Timeout: time.Second}
	return r
}

func restoreWAVersion(t *testing.T) {
	t.Helper()
	old := store.GetWAVersion()
	t.Cleanup(func() { store.SetWAVersion(old) })
}

func TestParseWAVersion(t *testing.T) {
	v, err := parseWAVersion([]byte(`[2, 3000, 1048620361]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != (store.WAVersionContainer{2, 3000, 1048620361}) {
		t.Fatalf("unexpected version: %s", v.String())
	}
	if _, err := parseWAVersion([]byte(`[2, 3000]`)); err == nil {
		t.Fatal("expected error for 2 parts")
	}
	if _, err := parseWAVersion([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestGreaterWAVersion(t *testing.T) {
	a := store.WAVersionContainer{2, 3000, 1049558099}
	b := store.WAVersionContainer{2, 3000, 1048620361}
	if greaterWAVersion(a, b) != a {
		t.Fatal("expected a (maior)")
	}
	if greaterWAVersion(b, a) != a {
		t.Fatal("expected a (maior) na ordem inversa")
	}
	if greaterWAVersion(a, a) != a {
		t.Fatal("empate deve retornar a")
	}
}

func TestParseWAVersionURLs(t *testing.T) {
	got := parseWAVersionURLs(" https://a/one.json , ,https://b/two.json ")
	if len(got) != 2 || got[0] != "https://a/one.json" || got[1] != "https://b/two.json" {
		t.Fatalf("unexpected urls: %#v", got)
	}
	if len(parseWAVersionURLs("")) != 0 {
		t.Fatal("expected empty list for blank input")
	}
}

func TestResolveWAVersionUsesCache(t *testing.T) {
	restoreWAVersion(t)
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`[2, 3000, 1049999999]`))
	}))
	defer srv.Close()

	r := testWAVersionResolver(srv.URL)
	r.base = store.WAVersionContainer{2, 3000, 1}

	first := r.Resolve(context.Background())
	if first != (store.WAVersionContainer{2, 3000, 1049999999}) {
		t.Fatalf("expected remote version, got %s", first.String())
	}
	second := r.Resolve(context.Background())
	if second != first {
		t.Fatalf("expected cached version, got %s", second.String())
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected 1 HTTP fetch (cache), got %d", got)
	}
	if store.GetWAVersion() != first {
		t.Fatalf("expected whatsmeow store to be updated, got %s", store.GetWAVersion().String())
	}
}

func TestResolveWAVersionRefetchesAfterTTL(t *testing.T) {
	restoreWAVersion(t)
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`[2, 3000, 1049999999]`))
	}))
	defer srv.Close()

	r := testWAVersionResolver(srv.URL)
	r.base = store.WAVersionContainer{2, 3000, 1}
	now := time.Now()
	r.now = func() time.Time { return now }

	r.Resolve(context.Background())
	now = now.Add(waVersionTTL + time.Minute)
	r.Resolve(context.Background())

	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("expected 2 HTTP fetches after TTL, got %d", got)
	}
}

func TestResolveWAVersionFallbackOnError(t *testing.T) {
	restoreWAVersion(t)
	r := testWAVersionResolver("http://127.0.0.1:1/unreachable")
	r.base = store.WAVersionContainer{2, 3000, 1049558099}

	got := r.Resolve(context.Background())
	if got != r.base {
		t.Fatalf("expected fallback, got %s", got.String())
	}
	if store.GetWAVersion() != r.base {
		t.Fatalf("expected fallback applied, got %s", store.GetWAVersion().String())
	}
}

func TestResolveWAVersionKeepsNewerEmbeddedVersion(t *testing.T) {
	restoreWAVersion(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[2, 3000, 1000000000]`))
	}))
	defer srv.Close()

	r := testWAVersionResolver(srv.URL)
	r.base = store.WAVersionContainer{2, 3000, 1049558099}

	got := r.Resolve(context.Background())
	if got != r.base {
		t.Fatalf("expected embedded version, got %s", got.String())
	}
}

func TestResolveWAVersionFallsBackToNextURL(t *testing.T) {
	restoreWAVersion(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[2, 3000, 1049999999]`))
	}))
	defer good.Close()

	r := testWAVersionResolver(bad.URL, good.URL)
	r.base = store.WAVersionContainer{2, 3000, 1}

	got := r.Resolve(context.Background())
	if got != (store.WAVersionContainer{2, 3000, 1049999999}) {
		t.Fatalf("expected version from second source, got %s", got.String())
	}
}
