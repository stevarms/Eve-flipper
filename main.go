//go:build !wails
// +build !wails

package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"eve-flipper/internal/api"
	"eve-flipper/internal/auth"
	"eve-flipper/internal/db"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/logger"
	"eve-flipper/internal/sde"
	"eve-flipper/internal/telemetry"
)

var version = "dev"

// defaultESIClientID and defaultESIClientSecret are populated for official
// release builds via -ldflags (see .github/workflows/release.yml). For local
// development and source builds they stay empty so SSO is effectively
// disabled unless ESI_CLIENT_ID / ESI_CLIENT_SECRET are provided via env.
var defaultESIClientID = ""
var defaultESIClientSecret = ""
var defaultESICallbackURL = "http://localhost:13370/api/auth/callback"

//go:embed frontend/dist/*
var frontendFS embed.FS

// resolveLogDir picks where eve-flipper-prod.log / -debug.log are written.
//
// Precedence:
//  1. EF_LOG_DIR env var — explicit override, always wins.
//  2. The running binary's directory — correct for release builds and
//     double-clicked binaries, where logs should sit next to the exe.
//  3. The current working directory — used when the exe lives in a Go build
//     cache temp dir, which is what `go run .` produces. Without this the dev
//     loop writes its logs into a throwaway temp folder that's deleted on exit
//     (and impossible to find), which defeats the point of file logging.
func resolveLogDir() string {
	if dir := strings.TrimSpace(os.Getenv("EF_LOG_DIR")); dir != "" {
		return dir
	}
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	exeDir := filepath.Dir(exePath)
	if exeDir == "" {
		return "."
	}
	// `go run` compiles into $TMPDIR/go-build*/…; detect that and fall back to
	// the working directory so dev-loop logs land in the repo.
	normalized := filepath.ToSlash(exeDir)
	if strings.Contains(normalized, "/go-build") {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil && cwd != "" {
			return cwd
		}
		return "."
	}
	return exeDir
}

func main() {
	// Load .env for double-clicked binaries / local builds. This is a no-op
	// when the file is absent, and never overrides existing OS env vars.
	loadDotEnv()

	if err := logger.InitFileLogging(resolveLogDir()); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] file logging init failed: %v\n", err)
	} else {
		defer logger.CloseFileLogging()
	}

	port := flag.Int("port", 13370, "HTTP server port")
	host := flag.String("host", "127.0.0.1", "Host to bind to (use 0.0.0.0 to allow LAN/remote access)")
	flag.Parse()

	logger.Banner(version)
	if prodLog, debugLog := logger.LogFiles(); prodLog != "" || debugLog != "" {
		if prodLog != "" {
			logger.Info("LOG", "Prod log file: "+prodLog)
		}
		if debugLog != "" {
			logger.Info("LOG", "Debug log file: "+debugLog)
		}
	}

	wd, _ := os.Getwd()
	dataDir := filepath.Join(wd, "data")
	os.MkdirAll(dataDir, 0755)

	// Open SQLite database
	database, err := db.Open()
	if err != nil {
		logger.Error("DB", fmt.Sprintf("Failed to open database: %v", err))
		os.Exit(1)
	}
	defer database.Close()

	// Migrate config.json → SQLite (if exists)
	database.MigrateFromJSON()

	// Run local cache cleanup after startup so large existing databases do not
	// block the app from becoming usable.
	database.CleanupStartupCachesAsync(30 * time.Second)

	// Load config from SQLite
	cfg := database.LoadConfig()

	esiClient := esi.NewClient(database)
	esiClient.LoadEVERefStructures() // background fetch of public structure names

	// ESI SSO config (from env vars or injected defaults for official builds).
	clientID := envOrDefault("ESI_CLIENT_ID", defaultESIClientID)
	clientSecret := envOrDefault("ESI_CLIENT_SECRET", defaultESIClientSecret)
	callbackURL := envOrDefault("ESI_CALLBACK_URL", defaultESICallbackURL)

	var ssoConfig *auth.SSOConfig
	if clientID != "" && clientSecret != "" {
		ssoConfig = &auth.SSOConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			CallbackURL:  callbackURL,
			Scopes: "esi-location.read_location.v1 esi-skills.read_skills.v1 esi-skills.read_skillqueue.v1 esi-wallet.read_character_wallet.v1 esi-assets.read_assets.v1 esi-characters.read_blueprints.v1 esi-industry.read_character_jobs.v1 esi-planets.manage_planets.v1 esi-markets.structure_markets.v1 esi-universe.read_structures.v1 esi-markets.read_character_orders.v1" +
				" esi-characters.read_corporation_roles.v1 esi-wallet.read_corporation_wallets.v1 esi-corporations.read_corporation_membership.v1 esi-corporations.read_blueprints.v1 esi-industry.read_corporation_jobs.v1 esi-industry.read_corporation_mining.v1 esi-markets.read_corporation_orders.v1 esi-corporations.read_divisions.v1 esi-corporations.track_members.v1 esi-assets.read_corporation_assets.v1" +
				" esi-ui.open_window.v1 esi-ui.write_waypoint.v1",
		}
	} else {
		logger.Info("SSO", "EVE SSO not configured (missing ESI_CLIENT_ID / ESI_CLIENT_SECRET)")
	}
	sessions := auth.NewSessionStore(database.SqlDB())
	database.SetPrivacyCodec(sessions.Vault())

	srv := api.NewServer(cfg, esiClient, database, ssoConfig, sessions)
	srv.SetAppVersion(version)
	srv.SetAppFlavor("web")
	srv.SetTelemetry(telemetry.NewFromEnv())

	// Load SDE in background
	go func() {
		data, err := sde.Load(dataDir)
		if err != nil {
			logger.Error("SDE", fmt.Sprintf("Load failed: %v", err))
			return
		}
		missingShipVolumes := prepareShipPackagedVolumes(dataDir, data)
		srv.SetSDE(data)
		logger.Success("SDE", "Scanner ready")
		refreshShipPackagedVolumesInBackground(dataDir, missingShipVolumes, esiClient)
	}()

	// Combine API + embedded frontend into a single handler
	apiHandler := srv.Handler()
	frontendContent, _ := fs.Sub(frontendFS, "frontend/dist")
	fileServer := http.FileServer(http.FS(frontendContent))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// Try static file, fall back to index.html (SPA)
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(frontendContent, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)
	logger.Server(addr)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Graceful shutdown on SIGINT / SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("Server", "Shutting down gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Server", fmt.Sprintf("Shutdown error: %v", err))
		}
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("Server", fmt.Sprintf("Failed: %v", err))
		os.Exit(1)
	}
	logger.Info("Server", "Stopped")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
