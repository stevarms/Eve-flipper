package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newStubOpenAIServer stands in for Ollama / LM Studio: an OpenAI-compatible server on
// loopback. httptest binds 127.0.0.1, so it also exercises the real host allowlist.
func newStubOpenAIServer(t *testing.T, seen *http.Header, body *map[string]interface{}) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		writeJSON(w, map[string]interface{}{
			"object": "list",
			"data": []map[string]string{
				{"id": "qwen2.5:7b"},
				{"id": "llama3.1:8b"},
				{"id": "llama3.1:8b"}, // duplicate, must be collapsed
				{"id": "  "},          // blank, must be dropped
			},
		})
	})

	// Long enough, and numeric enough, to survive stationAIValidateAnswer for a
	// trading-analysis intent — otherwise the handler falls into its retry path.
	answerParts := []string{
		"Tritanium at Jita IV - Moon 4 is still worth trading: it clears 2500000 ISK/day ",
		"at a 12.5% margin on 400 units of daily volume, with CTS 0.8 and no high-risk or ",
		"extreme-price flags. Keep min_item_profit at 1000000 ISK and min_daily_volume at 20, ",
		"then re-check after the next scan if margin drops under 8%.",
	}

	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		decoded := map[string]interface{}{}
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		if body != nil {
			*body = decoded
		}

		if decoded["stream"] != true {
			// Non-streaming path: the planner pass and the validation retry.
			writeJSON(w, map[string]interface{}{
				"id":    "local-1",
				"model": "llama3.1:8b",
				"choices": []map[string]interface{}{
					{"message": map[string]string{"content": strings.Join(answerParts, "")}},
				},
			})
			return
		}

		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range answerParts {
			chunk, _ := json.Marshal(map[string]interface{}{
				"id":    "local-1",
				"model": "llama3.1:8b",
				"choices": []map[string]interface{}{
					{"delta": map[string]string{"content": part}},
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleAuthStationAIModelsAgainstLocalServer(t *testing.T) {
	var seen http.Header
	stub := newStubOpenAIServer(t, &seen, nil)
	srv := &Server{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/station/ai/models", strings.NewReader(
		fmt.Sprintf(`{"provider":"ollama","base_url":%q}`, stub.URL),
	))
	srv.handleAuthStationAIModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider string   `json:"provider"`
		BaseURL  string   `json:"base_url"`
		Models   []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if resp.Provider != "ollama" {
		t.Fatalf("provider = %q", resp.Provider)
	}
	if resp.BaseURL != stub.URL+"/v1" {
		t.Fatalf("base_url = %q, want %q", resp.BaseURL, stub.URL+"/v1")
	}
	want := []string{"llama3.1:8b", "qwen2.5:7b"} // deduped, blank dropped, sorted
	if strings.Join(resp.Models, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", resp.Models, want)
	}
	// No key was supplied, so no Authorization header should have been sent, and the
	// OpenRouter attribution headers must not leak to a local server.
	if got := seen.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header = %q, want none", got)
	}
	if got := seen.Get("X-Title"); got != "" {
		t.Fatalf("X-Title header = %q, want none for a local provider", got)
	}
}

func TestHandleAuthStationAIModelsRejectsPublicHost(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/station/ai/models", strings.NewReader(
		`{"provider":"openai_compatible","base_url":"http://169.254.169.254/v1"}`,
	))
	srv.handleAuthStationAIModels(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestStationAIChatStreamAgainstLocalServer(t *testing.T) {
	var seen http.Header
	var sentBody map[string]interface{}
	stub := newStubOpenAIServer(t, &seen, &sentBody)
	srv := &Server{}

	// A trading question needs real scan context, or preflight short-circuits before the
	// provider is ever called.
	payload := fmt.Sprintf(`{
		"provider": "ollama",
		"base_url": %q,
		"model": "llama3.1:8b",
		"user_message": "should I keep trading this item?",
		"enable_planner": false,
		"enable_wiki_context": false,
		"enable_web_research": false,
		"context": {
			"system_name": "Jita",
			"station_scope": "Jita IV - Moon 4",
			"summary": {"total_rows": 1, "visible_rows": 1, "actionable_rows": 1},
			"rows": [{
				"type_id": 34,
				"type_name": "Tritanium",
				"station_name": "Jita IV - Moon 4",
				"cts": 0.8,
				"margin_percent": 12.5,
				"daily_profit": 2500000,
				"daily_volume": 400,
				"action": "buy"
			}]
		}
	}`, stub.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/station/ai/chat/stream", strings.NewReader(payload))
	srv.handleAuthStationAIChatStream(rec, req)

	out := rec.Body.String()
	if strings.Contains(out, `"type":"error"`) {
		t.Fatalf("stream produced an error: %s", out)
	}
	if !strings.Contains(out, "still worth trading") {
		t.Fatalf("stream did not carry the model output: %s", out)
	}
	if !strings.Contains(out, `"type":"result"`) {
		t.Fatalf("stream never emitted a result message: %s", out)
	}
	if !strings.Contains(out, `"provider":"ollama"`) {
		t.Fatalf("result did not report the local provider: %s", out)
	}
	// stream_options is OpenRouter-only; sending it can 400 some local servers.
	if _, ok := sentBody["stream_options"]; ok {
		t.Fatalf("stream_options must not be sent to a local provider: %v", sentBody)
	}
	if sentBody["stream"] != true {
		t.Fatalf("the streaming request never reached the provider: %v", sentBody)
	}
	if got := seen.Get("HTTP-Referer"); got != "" {
		t.Fatalf("HTTP-Referer = %q, want none for a local provider", got)
	}
}
