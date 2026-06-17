package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectHostedMaintenance(t *testing.T) {
	t.Setenv("TELEMETRY_ENABLED", "true")
	t.Setenv("TELEMETRY_ENV", "hosted")

	srv := &Server{}
	rec := httptest.NewRecorder()
	if !srv.rejectHostedMaintenance(rec, "demand refresh") {
		t.Fatal("expected hosted maintenance request to be rejected")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if !strings.Contains(rec.Body.String(), "disabled on hosted deployment") {
		t.Fatalf("response body %q does not explain hosted maintenance rejection", rec.Body.String())
	}

	t.Setenv(hostedMaintenanceOverrideEnv, "1")
	rec = httptest.NewRecorder()
	if srv.rejectHostedMaintenance(rec, "demand refresh") {
		t.Fatal("expected hosted maintenance override to allow request")
	}
}

func TestValidateDiscordWebhookURL(t *testing.T) {
	valid := []string{
		"https://discord.com/api/webhooks/123/token",
		"https://discordapp.com/api/webhooks/123/token",
	}
	for _, raw := range valid {
		if _, err := validateDiscordWebhookURL(raw); err != nil {
			t.Fatalf("validateDiscordWebhookURL(%q) error = %v, want nil", raw, err)
		}
	}

	invalid := []string{
		"",
		"http://discord.com/api/webhooks/123/token",
		"https://127.0.0.1/api/webhooks/123/token",
		"https://192.168.1.1/api/webhooks/123/token",
		"https://discord.com:444/api/webhooks/123/token",
		"https://discord.com/other/123/token",
		"https://user:pass@discord.com/api/webhooks/123/token",
	}
	for _, raw := range invalid {
		if _, err := validateDiscordWebhookURL(raw); err == nil {
			t.Fatalf("validateDiscordWebhookURL(%q) error = nil, want error", raw)
		}
	}
}
