package esi

import (
	"testing"
	"time"
)

type countingOrderRecorder struct {
	got chan MarketOrderSnapshot
}

func (r *countingOrderRecorder) RecordMarketOrderSnapshot(snapshot MarketOrderSnapshot) error {
	r.got <- snapshot
	return nil
}

func TestMarketOrderRecordingIsOffUntilSwitchedOn(t *testing.T) {
	// Archiving a region book is a ~400k-row SQLite write, and only the
	// Backtest tab ever reads it back. An install that never opens that tab
	// must not be paying for it, so attaching a recorder is not consent —
	// the switch is.
	recorder := &countingOrderRecorder{got: make(chan MarketOrderSnapshot, 4)}
	c := NewClient(nil)
	c.SetMarketOrderRecorder(recorder)

	if c.MarketOrderRecordingEnabled() {
		t.Fatal("recording reported on with only a recorder attached")
	}

	snapshot := MarketOrderSnapshot{
		RegionID: 10000002, OrderType: "sell", Source: "region",
		CapturedAt: time.Now().UTC(),
		Orders:     []MarketOrder{{OrderID: 1, TypeID: 34, LocationID: 60003760, Price: 5, VolumeRemain: 10}},
	}
	c.recordMarketOrderSnapshot(snapshot)
	select {
	case <-recorder.got:
		t.Fatal("snapshot was archived while recording is off")
	case <-time.After(150 * time.Millisecond):
	}

	c.SetMarketOrderRecordingEnabled(true)
	if !c.MarketOrderRecordingEnabled() {
		t.Fatal("recording did not report on after being switched on")
	}
	c.recordMarketOrderSnapshot(snapshot)
	select {
	case got := <-recorder.got:
		if got.RegionID != snapshot.RegionID || len(got.Orders) != 1 {
			t.Fatalf("archived %+v, want the snapshot we passed", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot was not archived after recording was switched on")
	}

	c.SetMarketOrderRecordingEnabled(false)
	if c.MarketOrderRecordingEnabled() {
		t.Fatal("recording still reports on after being switched off")
	}
}

func TestMarketOrderRecordingReportsOffWithoutARecorder(t *testing.T) {
	// The switch alone is not enough: a client with nowhere to write should
	// report off, so the UI never claims to be collecting replay material it
	// is actually dropping.
	c := NewClient(nil)
	c.SetMarketOrderRecordingEnabled(true)
	if c.MarketOrderRecordingEnabled() {
		t.Fatal("recording reported on with no recorder attached")
	}
}
