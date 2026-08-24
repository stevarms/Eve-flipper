package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// ndjsonEmitter serializes NDJSON writes to an http.ResponseWriter with a
// mutex + a context-cancel check so the "safe path" is the default. It
// replaces the ad-hoc header/flusher/sendProgress boilerplate that every
// streaming handler in this package used to hand-roll (audit finding
// P3.9 / DUPLICATION Cluster 3).
//
// TODO(audit-followup): as of this migration, only handleScan (radius)
// has been converted. The other seven NDJSON handlers — handleScanMultiRegion,
// handleScanRegionalDay, handleScanContracts, handleRouteFind,
// handleScanStation, and the achievements/demand streams — still hand-roll
// the same boilerplate. Migrate them opportunistically when touched for
// other reasons; the pattern is:
//   em, ok := beginNdjson(w, r); if !ok { return }
//   sendProgress := em.Progress
//   ... on error: em.Error(err.Error()); return
//   ... on success: em.Emit(resultEnvelope) — or em.Result(payload) for
//   the plain {type:"result", data:payload} shape.
//
// The scanner's fan-out handler (handleProfitableScan) in
// industry_blueprint_scan.go already had a mutex-guarded writer of its
// own; migration there also removes the local writeMu because the
// emitter's mu covers the same contract. See DUPLICATION Cluster 3.
//
// P3.10 (finalizeFlipScanResults) — the three flip-scan handlers
// (handleScan, handleScanMultiRegion, handleScanRegionalDay) share ~40
// lines of identical post-processing (filterFlipResults* → inventory
// enrich → KPI reduction → history insert → InsertFlipResults →
// processWatchlistAlerts → result emit). Extracting that into a shared
// helper is a similarly scoped follow-up; keeping the two changes
// separate makes each smaller to review.
//   - Content-Type + Cache-Control set once, in one place.
//   - Every write serialized behind a sync.Mutex (safe for handlers that
//     fan work into goroutines; a nop when only one goroutine writes).
//   - Every write short-circuits on <-ctx.Done() so a client disconnect
//     doesn't turn into a broken-pipe spam in the logs.
//   - Every write triggers a Flush so the client sees each line
//     immediately (NDJSON semantic).
//
// Typical usage:
//
//	em, ok := beginNdjson(w, r)
//	if !ok { return }
//	sendProgress := em.Progress  // pass this as the progress callback
//	// ... do work ...
//	em.Result(payload)
//
// When the caller wants a bare "message: string" progress line matching
// the historical shape, em.Progress works as-is. When the caller has a
// different shape (e.g. extra fields), call em.Emit(map).
type ndjsonEmitter struct {
	w   http.ResponseWriter
	f   http.Flusher
	ctx context.Context
	mu  sync.Mutex
}

// beginNdjson sets up the streaming headers and returns an emitter. When
// the response writer doesn't support flushing (rare — some middleware
// wrappers strip it) it writes a 500 error and returns ok=false; callers
// should return immediately in that case.
func beginNdjson(w http.ResponseWriter, r *http.Request) (*ndjsonEmitter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming not supported")
		return nil, false
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	return &ndjsonEmitter{
		w:   w,
		f:   flusher,
		ctx: r.Context(),
	}, true
}

// Emit writes any JSON-marshalable payload as one NDJSON line. It is the
// low-level primitive; Progress / Result / Error wrap it with the common
// `type` discriminant callers used before.
func (e *ndjsonEmitter) Emit(payload any) {
	if e == nil {
		return
	}
	if e.ctx != nil && e.ctx.Err() != nil {
		return
	}
	line, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[NDJSON] marshal error: %v", err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Re-check under the lock: a concurrent goroutine that saw the ctx
	// error first could have already returned; belt-and-braces for the
	// fan-out case.
	if e.ctx != nil && e.ctx.Err() != nil {
		return
	}
	fmt.Fprintf(e.w, "%s\n", line)
	e.f.Flush()
}

// Progress emits `{"type":"progress","message":msg}` — the shape every
// existing handler's sendProgress closure produced. Convert callers by
// passing em.Progress as the func(string) progress callback.
func (e *ndjsonEmitter) Progress(msg string) {
	e.Emit(map[string]string{"type": "progress", "message": msg})
}

// Error emits `{"type":"error","message":msg}` and terminates the stream
// convention-wise (callers should return after calling this).
func (e *ndjsonEmitter) Error(msg string) {
	e.Emit(map[string]string{"type": "error", "message": msg})
}

// Result emits a typed result envelope with the given payload. Matches
// the shape `{"type":"result", "data": payload}` several handlers use;
// callers that want a different envelope (e.g. embedded stats) should
// call Emit directly.
func (e *ndjsonEmitter) Result(payload any) {
	e.Emit(map[string]any{"type": "result", "data": payload})
}
