package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A NaN anywhere in a payload used to produce HTTP 200, Content-Type
// application/json, and an empty body — json.Encoder marshals into a buffer
// and writes nothing when marshalling fails, and writeJSON dropped the error.
// The client saw only "not valid JSON" with no endpoint named, and the server
// log stayed silent.
func TestWriteJSONReportsUnmarshalablePayloadInsteadOfEmpty200(t *testing.T) {
	nonFinite := []struct {
		name  string
		value float64
	}{
		{name: "NaN", value: math.NaN()},
		{name: "+Inf", value: math.Inf(1)},
		{name: "-Inf", value: math.Inf(-1)},
	}

	for _, tc := range nonFinite {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeJSON(rec, map[string]any{"margin": tc.value})

			if rec.Code == http.StatusOK {
				t.Fatalf("status = 200 with body %q, want a server error", rec.Body.String())
			}
			if rec.Body.Len() == 0 {
				t.Fatal("empty body: the client cannot tell this from a network fault")
			}
			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("error response is not itself JSON: %v (body %q)", err, rec.Body.String())
			}
			if payload["error"] == nil {
				t.Fatalf("error response carries no message: %q", rec.Body.String())
			}
		})
	}
}

func TestWriteJSONStillWritesGoodPayloads(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, map[string]any{"margin": 12.5, "ok": true})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["margin"] != 12.5 {
		t.Fatalf("margin = %v, want 12.5", payload["margin"])
	}
}

func TestWriteJSONStatusReportsUnmarshalablePayload(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONStatus(rec, http.StatusCreated, map[string]any{"eta": math.NaN()})

	if rec.Code == http.StatusCreated {
		t.Fatalf("status = 201 with body %q, want a server error", rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty body on an unmarshalable payload")
	}
}

func TestWriteJSONStatusPreservesStatusForGoodPayloads(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONStatus(rec, http.StatusCreated, map[string]any{"id": 7})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
}

// Go's ParseFloat accepts "NaN", and every comparison against NaN is false, so
// a range check written as `f < min || f > max` waves it straight through.
func TestDispositionQueryFloatRejectsNonFiniteInput(t *testing.T) {
	const def = 3.0

	for _, raw := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity"} {
		t.Run(raw, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/?target_eta_days="+raw, nil)
			got := dispositionQueryFloat(r, "target_eta_days", def, 0.5, 30)
			if got != def {
				t.Fatalf("dispositionQueryFloat(%q) = %v, want the default %v", raw, got, def)
			}
		})
	}
}

func TestDispositionQueryFloatStillAcceptsRealValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?target_eta_days=7.5", nil)
	if got := dispositionQueryFloat(r, "target_eta_days", 3, 0.5, 30); got != 7.5 {
		t.Fatalf("got %v, want 7.5", got)
	}

	// Out of range still falls back.
	r = httptest.NewRequest(http.MethodGet, "/?target_eta_days=900", nil)
	if got := dispositionQueryFloat(r, "target_eta_days", 3, 0.5, 30); got != 3 {
		t.Fatalf("got %v, want the default 3", got)
	}
}

func TestParseJournalFeeOverrideRejectsNonFiniteRates(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?sales_tax=NaN&broker_fee=1", nil)
	if got := parseJournalFeeOverride(r); got.set {
		t.Fatalf("NaN sales tax accepted as an override: %+v", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/?sales_tax=8&broker_fee=Inf", nil)
	if got := parseJournalFeeOverride(r); got.set {
		t.Fatalf("Inf broker fee accepted as an override: %+v", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/?sales_tax=8&broker_fee=1.5", nil)
	got := parseJournalFeeOverride(r)
	if !got.set || got.salesTax != 8 || got.brokerFee != 1.5 {
		t.Fatalf("real rates rejected: %+v", got)
	}
}
