// Package config loads runtime settings from the environment with sane
// defaults for local development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// HTTP listen address. Default 127.0.0.1:8787 (nginx proxies to it).
	Addr string
	// SQLite database file. Default ./data/gacha.db.
	DBPath string
	// Directory where collected cover images are written. Default ./data/covers.
	CoversDir string
	// Bilibili QR-login session file. Default ./data/bili-session.json.
	SessionPath string
	// BaseInterval: quiet-period per-game poll distance. Default 3h.
	RefreshInterval time.Duration
	// WatchInterval: poll distance inside an event watch window (off-peak
	// hours). Default 30m.
	WatchInterval time.Duration
	// PrimeInterval: poll distance inside a watch window during the hours
	// publishers actually post (07:00–14:00 / 18:00–23:00 CST). Default 15m.
	PrimeInterval time.Duration
	// Scheduler wake granularity. Default 5m.
	TickInterval time.Duration
	// Delay between collection cycles at startup (let migrations settle).
	StartupDelay time.Duration
	// Bearer token protecting /api/admin/*. Empty disables admin routes.
	AdminToken string
	// Public base URL used in API self-references (optional).
	PublicURL string
	// Collect at startup if the last cycle is older than RefreshInterval/2.
	CollectOnStart bool
	// Run the guided history backfill when the version table is thin.
	BootstrapOnStart bool
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// Load builds a Config from the environment.
func Load() (Config, error) {
	refreshInterval, err := envDuration("REFRESH_INTERVAL", 3*time.Hour)
	if err != nil {
		return Config{}, err
	}
	watchInterval, err := envDuration("WATCH_INTERVAL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	primeInterval, err := envDuration("PRIME_INTERVAL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	tickInterval, err := envDuration("TICK_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	startupDelay, err := envDuration("STARTUP_DELAY", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	dataDir := env("DATA_DIR", "data")
	return Config{
		Addr:             env("ADDR", "127.0.0.1:8787"),
		DBPath:           env("DB_PATH", dataDir+"/gacha.db"),
		CoversDir:        env("COVERS_DIR", dataDir+"/covers"),
		SessionPath:      env("SESSION_PATH", dataDir+"/bili-session.json"),
		RefreshInterval:  refreshInterval,
		WatchInterval:    watchInterval,
		PrimeInterval:    primeInterval,
		TickInterval:     tickInterval,
		StartupDelay:     startupDelay,
		AdminToken:       os.Getenv("ADMIN_TOKEN"),
		PublicURL:        os.Getenv("PUBLIC_URL"),
		CollectOnStart:   envBool("COLLECT_ON_START", true),
		BootstrapOnStart: envBool("BOOTSTRAP_ON_START", true),
	}, nil
}
