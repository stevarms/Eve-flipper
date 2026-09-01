package api

import (
	"net/http"
	"testing"
)

func TestStationAINormalizeBaseURL(t *testing.T) {
	valid := map[string]string{
		"http://127.0.0.1:11434":      "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/":     "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/v1":   "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/v1/":  "http://127.0.0.1:11434/v1",
		"  http://localhost:1234/v1 ": "http://localhost:1234/v1",
		"HTTP://127.0.0.1:8000":       "http://127.0.0.1:8000/v1",
		"http://192.168.1.50:11434":   "http://192.168.1.50:11434/v1",
	}
	for raw, want := range valid {
		got, err := stationAINormalizeBaseURL(raw)
		if err != nil {
			t.Fatalf("stationAINormalizeBaseURL(%q) error = %v, want nil", raw, err)
		}
		if got != want {
			t.Fatalf("stationAINormalizeBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"ftp://127.0.0.1:11434",
		"file:///etc/passwd",
		"http://user:pass@127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/v1?key=leak",
		"http://127.0.0.1:11434/v1#frag",
		"http:///v1",
	}
	for _, raw := range invalid {
		if got, err := stationAINormalizeBaseURL(raw); err == nil {
			t.Fatalf("stationAINormalizeBaseURL(%q) = %q, want error", raw, got)
		}
	}
}

func TestStationAIValidateLocalHost(t *testing.T) {
	allowed := []string{
		"http://127.0.0.1:11434/v1",
		"http://localhost:1234/v1",
		"http://[::1]:8000/v1",
		"http://192.168.1.50:11434/v1",
		"http://10.0.0.5:1234/v1",
		"http://172.16.4.4:8000/v1",
		"http://host.docker.internal:11434/v1",
	}
	for _, raw := range allowed {
		if err := stationAIValidateLocalHost(raw); err != nil {
			t.Fatalf("stationAIValidateLocalHost(%q) error = %v, want nil", raw, err)
		}
	}

	// 169.254.169.254 is the cloud instance-metadata endpoint: link-local, so it must be
	// rejected even though it is not routable on the public internet.
	blocked := []string{
		"http://169.254.169.254/v1",
		"http://[fe80::1]:8000/v1",
		"http://8.8.8.8/v1",
		"https://openrouter.ai/api/v1",
		"http://0.0.0.0:11434/v1",
		"http://224.0.0.1:11434/v1",
	}
	for _, raw := range blocked {
		if err := stationAIValidateLocalHost(raw); err == nil {
			t.Fatalf("stationAIValidateLocalHost(%q) error = nil, want error", raw)
		}
	}
}

func TestStationAIResolveProviderTarget(t *testing.T) {
	srv := &Server{}

	if _, errMsg := srv.stationAIResolveProviderTarget("nope", "", "key"); errMsg != "unsupported ai provider" {
		t.Fatalf("unknown provider error = %q, want %q", errMsg, "unsupported ai provider")
	}

	// OpenRouter still requires a key and ignores any client-supplied base URL.
	if _, errMsg := srv.stationAIResolveProviderTarget("openrouter", "", ""); errMsg != "api_key is required" {
		t.Fatalf("openrouter without key error = %q, want %q", errMsg, "api_key is required")
	}
	target, errMsg := srv.stationAIResolveProviderTarget("openrouter", "http://127.0.0.1:11434", "sk-or-test")
	if errMsg != "" {
		t.Fatalf("openrouter resolve error = %q, want none", errMsg)
	}
	if target.ChatURL != stationAIOpenRouterBaseURL+"/chat/completions" {
		t.Fatalf("openrouter chat url = %q, want pinned OpenRouter endpoint", target.ChatURL)
	}
	if !target.Spec.SendORHeaders || !target.Spec.SendStreamUsage {
		t.Fatalf("openrouter spec lost its attribution/usage flags: %+v", target.Spec)
	}

	// Local providers need no key and fall back to their default base URL.
	target, errMsg = srv.stationAIResolveProviderTarget("ollama", "", "")
	if errMsg != "" {
		t.Fatalf("ollama resolve error = %q, want none", errMsg)
	}
	if target.ChatURL != "http://127.0.0.1:11434/v1/chat/completions" {
		t.Fatalf("ollama chat url = %q", target.ChatURL)
	}
	if target.ModelsURL != "http://127.0.0.1:11434/v1/models" {
		t.Fatalf("ollama models url = %q", target.ModelsURL)
	}
	if target.Spec.SendORHeaders || target.Spec.SendStreamUsage {
		t.Fatalf("local provider must not send OpenRouter-specific fields: %+v", target.Spec)
	}
	if target.ChatTimeout <= stationAIRemoteChatTimeout {
		t.Fatalf("local chat timeout = %v, want longer than the remote default", target.ChatTimeout)
	}

	if _, errMsg = srv.stationAIResolveProviderTarget("lmstudio", "http://192.168.1.50:1234", ""); errMsg != "" {
		t.Fatalf("lmstudio LAN resolve error = %q, want none", errMsg)
	}

	// The custom entry has no default, so it must be told where to point.
	if _, errMsg = srv.stationAIResolveProviderTarget("openai_compatible", "", ""); errMsg == "" {
		t.Fatal("openai_compatible without base_url should be rejected")
	}
	if _, errMsg = srv.stationAIResolveProviderTarget("openai_compatible", "https://openrouter.ai/api/v1", ""); errMsg == "" {
		t.Fatal("openai_compatible pointed at a public host should be rejected")
	}
	if _, errMsg = srv.stationAIResolveProviderTarget("openai_compatible", "http://169.254.169.254/v1", ""); errMsg == "" {
		t.Fatal("openai_compatible pointed at cloud metadata should be rejected")
	}
}

func TestStationAILocalProvidersHostedGate(t *testing.T) {
	srv := &Server{}

	t.Setenv("EVEFLIPPER_HOSTED", "true")
	t.Setenv(stationAIAllowLocalProvidersEnv, "")
	if _, errMsg := srv.stationAIResolveProviderTarget("ollama", "", ""); errMsg != "local model providers are disabled on this deployment" {
		t.Fatalf("hosted local provider error = %q, want the hosted rejection", errMsg)
	}
	// OpenRouter keeps working on hosted deployments.
	if _, errMsg := srv.stationAIResolveProviderTarget("openrouter", "", "sk-or-test"); errMsg != "" {
		t.Fatalf("hosted openrouter resolve error = %q, want none", errMsg)
	}

	t.Setenv(stationAIAllowLocalProvidersEnv, "1")
	if _, errMsg := srv.stationAIResolveProviderTarget("ollama", "", ""); errMsg != "" {
		t.Fatalf("hosted local provider with opt-in error = %q, want none", errMsg)
	}
}

func TestNormalizeStationAIChatRequestProviders(t *testing.T) {
	// Local providers may omit the API key entirely.
	req := stationAIChatRequestPayload{
		Provider:    "ollama",
		Model:       "llama3.1:8b",
		UserMessage: "hello",
	}
	if _, _, _, errMsg := normalizeStationAIChatRequest(&req); errMsg != "" {
		t.Fatalf("ollama without api key error = %q, want none", errMsg)
	}

	// OpenRouter still does not.
	req = stationAIChatRequestPayload{
		Provider:    "openrouter",
		Model:       "openai/gpt-4o-mini",
		UserMessage: "hello",
	}
	if _, _, _, errMsg := normalizeStationAIChatRequest(&req); errMsg != "api_key is required" {
		t.Fatalf("openrouter without api key error = %q, want %q", errMsg, "api_key is required")
	}

	// Empty provider still defaults to OpenRouter for older clients.
	req = stationAIChatRequestPayload{
		Model:       "openai/gpt-4o-mini",
		APIKey:      "sk-or-test",
		UserMessage: "hello",
	}
	if _, _, _, errMsg := normalizeStationAIChatRequest(&req); errMsg != "" {
		t.Fatalf("default provider error = %q, want none", errMsg)
	}
	if req.Provider != "openrouter" {
		t.Fatalf("default provider = %q, want openrouter", req.Provider)
	}

	req = stationAIChatRequestPayload{
		Provider:    "gemini",
		Model:       "whatever",
		UserMessage: "hello",
	}
	if _, _, _, errMsg := normalizeStationAIChatRequest(&req); errMsg != "unsupported ai provider" {
		t.Fatalf("unknown provider error = %q, want %q", errMsg, "unsupported ai provider")
	}
}

func TestStationAIApplyProviderHeaders(t *testing.T) {
	srv := &Server{}

	orTarget, errMsg := srv.stationAIResolveProviderTarget("openrouter", "", "sk-or-test")
	if errMsg != "" {
		t.Fatalf("openrouter resolve error = %q", errMsg)
	}
	orReq, err := http.NewRequest(http.MethodPost, orTarget.ChatURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	stationAIApplyProviderHeaders(orReq, orTarget, "EVE Flipper Station AI")
	if got := orReq.Header.Get("Authorization"); got != "Bearer sk-or-test" {
		t.Fatalf("openrouter Authorization = %q", got)
	}
	if got := orReq.Header.Get("X-Title"); got != "EVE Flipper Station AI" {
		t.Fatalf("openrouter X-Title = %q", got)
	}
	if got := orReq.Header.Get("HTTP-Referer"); got == "" {
		t.Fatal("openrouter HTTP-Referer must still be sent")
	}

	localTarget, errMsg := srv.stationAIResolveProviderTarget("lmstudio", "", "")
	if errMsg != "" {
		t.Fatalf("lmstudio resolve error = %q", errMsg)
	}
	localReq, err := http.NewRequest(http.MethodPost, localTarget.ChatURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	stationAIApplyProviderHeaders(localReq, localTarget, "EVE Flipper Station AI")
	if got := localReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("local Authorization = %q, want none when no key is set", got)
	}
	if got := localReq.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("local Content-Type = %q", got)
	}

	// A key the user did supply is still forwarded — some local servers require one.
	keyedTarget, errMsg := srv.stationAIResolveProviderTarget("lmstudio", "", "lm-studio")
	if errMsg != "" {
		t.Fatalf("lmstudio keyed resolve error = %q", errMsg)
	}
	keyedReq, err := http.NewRequest(http.MethodPost, keyedTarget.ChatURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	stationAIApplyProviderHeaders(keyedReq, keyedTarget, "EVE Flipper Station AI")
	if got := keyedReq.Header.Get("Authorization"); got != "Bearer lm-studio" {
		t.Fatalf("local keyed Authorization = %q", got)
	}
}
