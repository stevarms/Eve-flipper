package esi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// probeHealth must distinguish "we could not reach ESI" from "ESI answered".
// A throttled answer still proves the network path works, and treating it as an
// outage blanked the whole UI behind a modal.
func TestProbeHealthClassifiesResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantOK     bool
		wantReason bool
	}{
		{name: "ok", statusCode: http.StatusOK, wantOK: true, wantReason: false},
		{name: "esi error limited", statusCode: 420, wantOK: true, wantReason: true},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, wantOK: true, wantReason: true},
		{name: "server error", statusCode: http.StatusServiceUnavailable, wantOK: false, wantReason: true},
		{name: "not found", statusCode: http.StatusNotFound, wantOK: false, wantReason: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(`{"players":1}`))
			}))
			defer srv.Close()

			c := NewClient(nil)
			ok, reason := c.probeHealth(srv.URL)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason %q)", ok, tc.wantOK, reason)
			}
			if (reason != "") != tc.wantReason {
				t.Fatalf("reason = %q, want non-empty = %v", reason, tc.wantReason)
			}
		})
	}
}

func TestProbeHealthReportsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	c := NewClient(nil)
	ok, reason := c.probeHealth(url)
	if ok {
		t.Fatal("probeHealth reported healthy against a closed listener")
	}
	if reason == "" {
		t.Fatal("probeHealth gave no reason for an unreachable endpoint")
	}
}

// HealthCheck used to hold healthMu across the HTTP round trip, so every
// concurrent /api/status serialized behind a live network call. Callers that
// arrive mid-probe must get the cached value back promptly instead.
func TestHealthCheckDoesNotBlockConcurrentCallersOnTheProbe(t *testing.T) {
	release := make(chan struct{})
	var hits int
	var hitMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitMu.Lock()
		hits++
		hitMu.Unlock()
		<-release // hold the probe open
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(nil)

	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		c.probeHealthAndStore(srv.URL)
	}()

	// Wait until the probe is definitely in flight and holding the connection.
	deadline := time.Now().Add(2 * time.Second)
	for {
		hitMu.Lock()
		started := hits > 0
		hitMu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe never reached the test server")
		}
		time.Sleep(time.Millisecond)
	}

	// A reader must not block behind the in-flight probe.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = c.HealthStatus()
		_ = c.HealthError()
	}()

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("HealthStatus blocked while a probe was in flight")
	}

	close(release)
	<-probeDone

	if ok, _ := c.HealthStatus(); !ok {
		t.Fatal("health not recorded as ok after a 200 probe")
	}
}

// The 10s cache must stop /api/status polling from turning into ESI traffic.
func TestHealthCheckCachesResult(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(nil)
	for i := 0; i < 5; i++ {
		c.probeHealthAndStore(srv.URL)
	}

	mu.Lock()
	got := hits
	mu.Unlock()
	if got != 1 {
		t.Fatalf("probe ran %d times through the cache, want 1", got)
	}
}
