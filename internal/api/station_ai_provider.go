package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Station AI supports OpenRouter plus any OpenAI-compatible server the user runs locally
// (Ollama, LM Studio, Unsloth Studio / vLLM). All of them speak the same
// POST {base}/chat/completions and GET {base}/models shapes, so the only per-provider
// differences are the base URL, whether an API key is mandatory, and a couple of
// OpenRouter-specific request quirks.

const stationAIAllowLocalProvidersEnv = "STATION_AI_ALLOW_LOCAL_PROVIDERS"

const stationAIOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// Local inference on CPU is far slower than a hosted API, so local targets get
// generous deadlines.
const stationAILocalChatTimeout = stationAIStreamHTTPTimeout
const stationAILocalPlannerTimeout = 3 * time.Minute
const stationAIRemoteChatTimeout = 90 * time.Second
const stationAIRemotePlannerTimeout = 35 * time.Second
const stationAIModelsTimeout = 10 * time.Second

const stationAIModelsMaxBodyBytes int64 = 1024 * 1024

// dockerHostAlias is how a containerized eve-flipper reaches a model server running on
// the Docker host. It resolves to a host-only address, never to the public internet.
const dockerHostAlias = "host.docker.internal"

type stationAIProviderSpec struct {
	ID              string
	Label           string
	Local           bool
	RequiresAPIKey  bool
	DefaultBaseURL  string
	SendORHeaders   bool // OpenRouter's HTTP-Referer / X-Title attribution headers
	SendStreamUsage bool // stream_options.include_usage — not universally supported
}

var stationAIProviderSpecs = map[string]stationAIProviderSpec{
	"openrouter": {
		ID:              "openrouter",
		Label:           "OpenRouter",
		RequiresAPIKey:  true,
		DefaultBaseURL:  stationAIOpenRouterBaseURL,
		SendORHeaders:   true,
		SendStreamUsage: true,
	},
	"ollama": {
		ID:             "ollama",
		Label:          "Ollama",
		Local:          true,
		DefaultBaseURL: "http://127.0.0.1:11434/v1",
	},
	"lmstudio": {
		ID:             "lmstudio",
		Label:          "LM Studio",
		Local:          true,
		DefaultBaseURL: "http://127.0.0.1:1234/v1",
	},
	"unsloth": {
		ID:    "unsloth",
		Label: "Unsloth Studio",
		Local: true,
		// Unsloth Studio serves through vLLM by default, which listens on :8000.
		// Installations that front it with llama.cpp use a different port, so the
		// base URL is editable in the UI.
		DefaultBaseURL: "http://127.0.0.1:8000/v1",
	},
	"openai_compatible": {
		ID:    "openai_compatible",
		Label: "OpenAI-compatible (custom)",
		Local: true,
		// No default: the user must say where their server lives.
	},
}

// stationAIProviderTarget is the resolved, validated endpoint for one chat request.
type stationAIProviderTarget struct {
	Spec           stationAIProviderSpec
	BaseURL        string
	ChatURL        string
	ModelsURL      string
	APIKey         string
	ChatTimeout    time.Duration
	PlannerTimeout time.Duration
}

func stationAIProviderSpecByID(id string) (stationAIProviderSpec, bool) {
	spec, ok := stationAIProviderSpecs[strings.ToLower(strings.TrimSpace(id))]
	return spec, ok
}

// stationAINormalizeBaseURL turns whatever the user typed into a bare OpenAI-style
// `/v1` root: no credentials, no query, no trailing slash.
func stationAINormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base url")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("base url must use http or https")
	}
	if u.User != nil {
		return "", fmt.Errorf("base url must not include credentials")
	}
	if strings.TrimSpace(u.RawQuery) != "" || strings.TrimSpace(u.Fragment) != "" {
		return "", fmt.Errorf("base url must not include a query or fragment")
	}
	if strings.TrimSpace(u.Hostname()) == "" {
		return "", fmt.Errorf("base url must include a host")
	}

	path := strings.TrimRight(u.EscapedPath(), "/")
	if path == "" || !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}

	normalized := url.URL{
		Scheme: strings.ToLower(u.Scheme),
		Host:   u.Host,
		Path:   path,
	}
	return normalized.String(), nil
}

// stationAIValidateLocalHost keeps the "point Ivy at your own model server" feature from
// doubling as a server-side request forgery primitive. Only loopback, RFC1918/ULA private
// ranges and the Docker host alias are reachable.
func stationAIValidateLocalHost(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid base url")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("base url must include a host")
	}
	if isLoopbackHost(host) || host == dockerHostAlias {
		return nil
	}

	var ips []net.IP
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IP{literal}
	} else {
		resolved, lookupErr := net.LookupIP(host)
		if lookupErr != nil || len(resolved) == 0 {
			return fmt.Errorf("could not resolve %q", host)
		}
		ips = resolved
	}

	// Every resolved address must be private. Checking all of them — not just the first —
	// is what defeats a DNS-rebinding name that mixes a private and a public answer.
	for _, ip := range ips {
		if !isStationAIReachableLocalIP(ip) {
			return fmt.Errorf("base url host must be a loopback or private-network address")
		}
	}
	return nil
}

func isStationAIReachableLocalIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// 169.254.0.0/16 and fe80::/10 cover the cloud instance-metadata endpoints.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// stationAIMaxRedirects is generous for an OpenAI-compatible endpoint, which should
// not be redirecting at all, while still terminating a loop.
const stationAIMaxRedirects = 5

// stationAIDialTimeout bounds the connect phase separately from the overall request,
// which for a local chat completion is measured in minutes.
const stationAIDialTimeout = 10 * time.Second

// stationAIHTTPClient builds the client every provider call goes through.
//
// stationAIValidateLocalHost checks the base URL at the moment it is accepted, which
// leaves two gaps for a local target. A hostname can resolve to a private address for
// that check and a public one by the time the connection is actually made; and a
// cooperating server on a private address can answer 302 to anywhere it likes, which
// the default client would follow. Both are closed here, where the address being
// reached is finally known.
func stationAIHTTPClient(target stationAIProviderTarget, timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	if !target.Spec.Local {
		// Remote providers run against a base URL pinned in the spec, never a
		// client-supplied one, so there is nothing here to forge.
		return client
	}

	dialer := &net.Dialer{Timeout: stationAIDialTimeout, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		// Whatever the name resolved to earlier, this is the peer we got. The
		// Docker host alias is included deliberately: it is documented as
		// resolving to a host-only address, so the check can only ever agree
		// with that — and if it does not, refusing is the right answer.
		ip := stationAIConnRemoteIP(conn)
		if !isStationAIReachableLocalIP(ip) {
			addrStr := "unknown address"
			if remote := conn.RemoteAddr(); remote != nil {
				addrStr = remote.String()
			}
			conn.Close()
			return nil, fmt.Errorf("refusing to connect to non-local address %s", addrStr)
		}
		return conn, nil
	}
	client.Transport = transport

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= stationAIMaxRedirects {
			return fmt.Errorf("too many redirects from local model provider")
		}
		if err := stationAIValidateLocalHost(req.URL.String()); err != nil {
			return fmt.Errorf("refusing redirect to %s: %w", req.URL.Host, err)
		}
		return nil
	}
	return client
}

func stationAIConnRemoteIP(conn net.Conn) net.IP {
	if conn == nil {
		return nil
	}
	switch addr := conn.RemoteAddr().(type) {
	case *net.TCPAddr:
		return addr.IP
	case *net.UDPAddr:
		return addr.IP
	case *net.IPAddr:
		return addr.IP
	}
	return nil
}

func stationAILocalProvidersAllowed(s *Server) bool {
	if s == nil || !s.isHostedDeployment() {
		return true
	}
	return envFlagEnabled(stationAIAllowLocalProvidersEnv)
}

// stationAIResolveProviderTarget validates the provider/base-URL/api-key triple and returns
// the concrete endpoints to call. The second return value is a user-facing error string
// ("" on success), matching the convention of normalizeStationAIChatRequest.
func (s *Server) stationAIResolveProviderTarget(provider, baseURL, apiKey string) (stationAIProviderTarget, string) {
	spec, ok := stationAIProviderSpecByID(provider)
	if !ok {
		return stationAIProviderTarget{}, "unsupported ai provider"
	}
	apiKey = strings.TrimSpace(apiKey)
	if spec.RequiresAPIKey && apiKey == "" {
		return stationAIProviderTarget{}, "api_key is required"
	}

	target := stationAIProviderTarget{
		Spec:           spec,
		APIKey:         apiKey,
		ChatTimeout:    stationAIRemoteChatTimeout,
		PlannerTimeout: stationAIRemotePlannerTimeout,
	}

	if !spec.Local {
		// Remote providers are pinned; a client-supplied base URL is ignored rather than
		// trusted.
		target.BaseURL = spec.DefaultBaseURL
	} else {
		if !stationAILocalProvidersAllowed(s) {
			return stationAIProviderTarget{}, "local model providers are disabled on this deployment"
		}
		raw := strings.TrimSpace(baseURL)
		if raw == "" {
			raw = spec.DefaultBaseURL
		}
		if raw == "" {
			return stationAIProviderTarget{}, "base_url is required for this provider"
		}
		normalized, err := stationAINormalizeBaseURL(raw)
		if err != nil {
			return stationAIProviderTarget{}, err.Error()
		}
		if err := stationAIValidateLocalHost(normalized); err != nil {
			return stationAIProviderTarget{}, err.Error()
		}
		target.BaseURL = normalized
		target.ChatTimeout = stationAILocalChatTimeout
		target.PlannerTimeout = stationAILocalPlannerTimeout
	}

	target.ChatURL = target.BaseURL + "/chat/completions"
	target.ModelsURL = target.BaseURL + "/models"
	return target, ""
}

func stationAIApplyProviderHeaders(httpReq *http.Request, target stationAIProviderTarget, title string) {
	if httpReq == nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(target.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	if target.Spec.SendORHeaders {
		httpReq.Header.Set("HTTP-Referer", "http://localhost:1420")
		httpReq.Header.Set("X-Title", title)
	}
}

// stationAIProviderLabel names the provider in user-visible progress messages.
func stationAIProviderLabel(target stationAIProviderTarget) string {
	if label := strings.TrimSpace(target.Spec.Label); label != "" {
		return label
	}
	return "the model provider"
}

type stationAIModelsRequestPayload struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
}

// handleAuthStationAIModels lists the models a provider is serving. Ollama, LM Studio and
// vLLM all expose the OpenAI `/v1/models` shape, so one code path covers every provider.
// It doubles as the "test connection" action in the config modal.
func (s *Server) handleAuthStationAIModels(w http.ResponseWriter, r *http.Request) {
	var req stationAIModelsRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	target, resolveErr := s.stationAIResolveProviderTarget(req.Provider, req.BaseURL, req.APIKey)
	if resolveErr != "" {
		writeError(w, http.StatusBadRequest, resolveErr)
		return
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.ModelsURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create model list request")
		return
	}
	stationAIApplyProviderHeaders(httpReq, target, "EVE Flipper Station AI Models")

	client := stationAIHTTPClient(target, stationAIModelsTimeout)
	resp, err := client.Do(httpReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach "+stationAIProviderLabel(target)+" at "+target.BaseURL)
		return
	}
	defer resp.Body.Close()

	body, readErr := readBodyWithLimit(resp.Body, stationAIModelsMaxBodyBytes)
	if readErr != nil {
		writeError(w, http.StatusBadGateway, "failed to read model list response")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := "model list request failed"
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && strings.TrimSpace(errBody.Error.Message) != "" {
			errMsg = strings.TrimSpace(errBody.Error.Message)
		}
		writeError(w, http.StatusBadGateway, errMsg)
		return
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeError(w, http.StatusBadGateway, "invalid model list response")
		return
	}

	models := make([]string, 0, len(parsed.Data))
	seen := make(map[string]bool, len(parsed.Data))
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)

	writeJSON(w, map[string]interface{}{
		"provider": target.Spec.ID,
		"base_url": target.BaseURL,
		"models":   models,
	})
}
